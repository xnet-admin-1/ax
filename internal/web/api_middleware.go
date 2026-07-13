package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"
)

// settings holds application-level config referenced by middleware.
var settings struct {
	Model string
}

// tokenUsedKey is a context-free approach: store tokens used in response header
// before withMetadata runs its post-processing. Handlers set this header themselves.
const headerTokensUsed = "X-Tokens-Used"

// withMetadata wraps an http.HandlerFunc to inject response metadata headers.
func withMetadata(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		next(w, r)

		elapsed := time.Since(start).Milliseconds()
		w.Header().Set("X-Request-Time-Ms", fmt.Sprintf("%d", elapsed))

		if settings.Model != "" {
			w.Header().Set("X-Model", settings.Model)
		}

		// X-Tokens-Used is set by the handler if token counting is available.
		// We leave it intact if already set; nothing to do here.
	}
}

// negotiateFormat inspects the Accept header and writes data in the requested format.
func negotiateFormat(r *http.Request, data any, w http.ResponseWriter) {
	accept := r.Header.Get("Accept")

	switch {
	case strings.Contains(accept, "application/yaml"):
		w.Header().Set("Content-Type", "application/yaml")
		w.Write([]byte(toYAML(data)))

	case strings.Contains(accept, "text/plain"):
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(fmt.Sprintf("%v", data)))

	case strings.Contains(accept, "text/markdown"):
		w.Header().Set("Content-Type", "text/markdown")
		w.Write([]byte(toMarkdownTable(data)))

	default:
		// application/json is the default
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.Encode(data)
	}
}

// toYAML produces simple key: value YAML without external dependencies.
// For maps and structs it emits one line per field. For slices it uses "- item" syntax.
func toYAML(data any) string {
	var sb strings.Builder
	writeYAML(&sb, data, 0)
	return sb.String()
}

func writeYAML(sb *strings.Builder, data any, indent int) {
	prefix := strings.Repeat("  ", indent)

	if data == nil {
		sb.WriteString(prefix + "null\n")
		return
	}

	v := reflect.ValueOf(data)
	switch v.Kind() {
	case reflect.Map:
		for _, key := range v.MapKeys() {
			val := v.MapIndex(key)
			if isScalar(val) {
				sb.WriteString(fmt.Sprintf("%s%v: %v\n", prefix, key, val.Interface()))
			} else {
				sb.WriteString(fmt.Sprintf("%s%v:\n", prefix, key))
				writeYAML(sb, val.Interface(), indent+1)
			}
		}
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}
			val := v.Field(i)
			name := field.Tag.Get("json")
			if name == "" || name == "-" {
				name = field.Name
			}
			// Strip json tag options like ",omitempty"
			if idx := strings.Index(name, ","); idx != -1 {
				name = name[:idx]
			}
			if isScalar(val) {
				sb.WriteString(fmt.Sprintf("%s%s: %v\n", prefix, name, val.Interface()))
			} else {
				sb.WriteString(fmt.Sprintf("%s%s:\n", prefix, name))
				writeYAML(sb, val.Interface(), indent+1)
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			elem := v.Index(i)
			if isScalar(elem) {
				sb.WriteString(fmt.Sprintf("%s- %v\n", prefix, elem.Interface()))
			} else {
				sb.WriteString(prefix + "-\n")
				writeYAML(sb, elem.Interface(), indent+1)
			}
		}
	case reflect.Ptr, reflect.Interface:
		if v.IsNil() {
			sb.WriteString(prefix + "null\n")
		} else {
			writeYAML(sb, v.Elem().Interface(), indent)
		}
	default:
		sb.WriteString(fmt.Sprintf("%s%v\n", prefix, data))
	}
}

func isScalar(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Map, reflect.Struct, reflect.Slice, reflect.Array:
		return false
	case reflect.Ptr, reflect.Interface:
		if v.IsNil() {
			return true
		}
		return isScalar(v.Elem())
	default:
		return true
	}
}

// toMarkdownTable renders data as a markdown table.
// For a slice of maps/structs it creates rows; for a single object it creates a key|value table.
func toMarkdownTable(data any) string {
	if data == nil {
		return "| (empty) |\n"
	}

	v := reflect.ValueOf(data)

	// Unwrap pointer/interface
	for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return "| (empty) |\n"
		}
		v = v.Elem()
	}

	switch v.Kind() {
	case reflect.Map:
		return mapToMarkdown(v)
	case reflect.Struct:
		return structToMarkdown(v)
	case reflect.Slice, reflect.Array:
		if v.Len() == 0 {
			return "| (empty) |\n"
		}
		first := v.Index(0)
		for first.Kind() == reflect.Ptr || first.Kind() == reflect.Interface {
			first = first.Elem()
		}
		if first.Kind() == reflect.Struct {
			return structSliceToMarkdown(v)
		}
		return sliceToMarkdown(v)
	default:
		return fmt.Sprintf("| Value |\n|---|\n| %v |\n", data)
	}
}

func mapToMarkdown(v reflect.Value) string {
	var sb strings.Builder
	sb.WriteString("| Key | Value |\n|---|---|\n")
	for _, key := range v.MapKeys() {
		sb.WriteString(fmt.Sprintf("| %v | %v |\n", key, v.MapIndex(key).Interface()))
	}
	return sb.String()
}

func structToMarkdown(v reflect.Value) string {
	var sb strings.Builder
	t := v.Type()
	sb.WriteString("| Field | Value |\n|---|---|\n")
	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		sb.WriteString(fmt.Sprintf("| %s | %v |\n", field.Name, v.Field(i).Interface()))
	}
	return sb.String()
}

func structSliceToMarkdown(v reflect.Value) string {
	var sb strings.Builder
	first := v.Index(0)
	for first.Kind() == reflect.Ptr || first.Kind() == reflect.Interface {
		first = first.Elem()
	}
	t := first.Type()

	// Header
	var headers []string
	for i := 0; i < t.NumField(); i++ {
		if t.Field(i).IsExported() {
			headers = append(headers, t.Field(i).Name)
		}
	}
	sb.WriteString("| " + strings.Join(headers, " | ") + " |\n")
	sb.WriteString("|" + strings.Repeat("---|", len(headers)) + "\n")

	// Rows
	for i := 0; i < v.Len(); i++ {
		elem := v.Index(i)
		for elem.Kind() == reflect.Ptr || elem.Kind() == reflect.Interface {
			elem = elem.Elem()
		}
		var vals []string
		for j := 0; j < elem.NumField(); j++ {
			if elem.Type().Field(j).IsExported() {
				vals = append(vals, fmt.Sprintf("%v", elem.Field(j).Interface()))
			}
		}
		sb.WriteString("| " + strings.Join(vals, " | ") + " |\n")
	}
	return sb.String()
}

func sliceToMarkdown(v reflect.Value) string {
	var sb strings.Builder
	sb.WriteString("| # | Value |\n|---|---|\n")
	for i := 0; i < v.Len(); i++ {
		sb.WriteString(fmt.Sprintf("| %d | %v |\n", i+1, v.Index(i).Interface()))
	}
	return sb.String()
}

// ---------- Rate Limiter ----------

// rateLimiter implements an in-memory token bucket per IP address.
// Default: 100 requests per minute.
type rateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	rate     int           // tokens added per interval
	interval time.Duration // refill interval
	burst    int           // max tokens (bucket capacity)
}

type bucket struct {
	tokens   int
	lastTime time.Time
}

// newRateLimiter creates a rate limiter with the given requests-per-minute limit.
func newRateLimiter(requestsPerMinute int) *rateLimiter {
	return &rateLimiter{
		buckets:  make(map[string]*bucket),
		rate:     requestsPerMinute,
		interval: time.Minute,
		burst:    requestsPerMinute,
	}
}

// defaultRateLimiter is the package-level limiter: 100 requests/minute.
var defaultRateLimiter = newRateLimiter(100)

// Allow checks whether the given IP is allowed to make a request.
func (rl *rateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, exists := rl.buckets[ip]
	if !exists {
		rl.buckets[ip] = &bucket{
			tokens:   rl.burst - 1,
			lastTime: now,
		}
		return true
	}

	// Refill tokens based on elapsed time
	elapsed := now.Sub(b.lastTime)
	refill := int(elapsed.Seconds() * float64(rl.rate) / 60.0)
	if refill > 0 {
		b.tokens += refill
		if b.tokens > rl.burst {
			b.tokens = rl.burst
		}
		b.lastTime = now
	}

	if b.tokens > 0 {
		b.tokens--
		return true
	}

	return false
}
