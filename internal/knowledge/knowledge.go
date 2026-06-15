package knowledge

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
)

type Store struct {
	DB *sql.DB
}

type Result struct {
	Path  string
	Chunk string
	Score float64
}

func NewStore(db *sql.DB) *Store {
	db.Exec(`CREATE TABLE IF NOT EXISTS knowledge_docs(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		path TEXT NOT NULL,
		chunk TEXT NOT NULL,
		embedding BLOB
	)`)
	return &Store{DB: db}
}

func (s *Store) Add(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	chunks := splitChunks(string(data))
	for _, c := range chunks {
		_, err := s.DB.Exec("INSERT INTO knowledge_docs(path, chunk) VALUES(?, ?)", path, c)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Search(query string, limit int) ([]Result, error) {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return nil, nil
	}
	// Build query: count matching words per chunk
	var conditions []string
	var args []interface{}
	for _, w := range words {
		conditions = append(conditions, "CASE WHEN LOWER(chunk) LIKE ? THEN 1 ELSE 0 END")
		args = append(args, "%"+w+"%")
	}
	scoreExpr := strings.Join(conditions, " + ")
	q := fmt.Sprintf("SELECT path, chunk, (%s) as score FROM knowledge_docs WHERE score > 0 ORDER BY score DESC LIMIT ?", scoreExpr)
	args = append(args, limit)

	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []Result
	for rows.Next() {
		var r Result
		rows.Scan(&r.Path, &r.Chunk, &r.Score)
		results = append(results, r)
	}
	return results, nil
}

func (s *Store) List() ([]string, error) {
	rows, err := s.DB.Query("SELECT DISTINCT path FROM knowledge_docs")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var p string
		rows.Scan(&p)
		paths = append(paths, p)
	}
	return paths, nil
}

func (s *Store) Delete(path string) error {
	_, err := s.DB.Exec("DELETE FROM knowledge_docs WHERE path = ?", path)
	return err
}

func (s *Store) Stats() (int, int) {
	var docs, chunks int
	s.DB.QueryRow("SELECT COUNT(DISTINCT path) FROM knowledge_docs").Scan(&docs)
	s.DB.QueryRow("SELECT COUNT(*) FROM knowledge_docs").Scan(&chunks)
	return docs, chunks
}

func splitChunks(text string) []string {
	// Split on double newlines first
	parts := strings.Split(text, "\n\n")
	var chunks []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if len(p) <= 500 {
			chunks = append(chunks, p)
		} else {
			for i := 0; i < len(p); i += 500 {
				end := i + 500
				if end > len(p) {
					end = len(p)
				}
				chunks = append(chunks, p[i:end])
			}
		}
	}
	return chunks
}
