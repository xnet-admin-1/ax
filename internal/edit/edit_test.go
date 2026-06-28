package edit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApply_ExactMatch(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "test.go")
	os.WriteFile(f, []byte("func hello() {\n\tfmt.Println(\"hello\")\n}\n"), 0644)

	err := Apply(f, "func hello() {\n\tfmt.Println(\"hello\")\n}", "func hello() {\n\tfmt.Println(\"world\")\n}")
	if err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile(f)
	want := "func hello() {\n\tfmt.Println(\"world\")\n}\n"
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestApply_IndentFlexible(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "test.go")
	// File has 2-tab indent
	os.WriteFile(f, []byte("func main() {\n\t\tif true {\n\t\t\tfmt.Println(\"hi\")\n\t\t}\n}\n"), 0644)

	// Search has 1-tab indent (LLM got indentation wrong)
	err := Apply(f, "\tif true {\n\t\tfmt.Println(\"hi\")\n\t}", "\tif true {\n\t\tfmt.Println(\"bye\")\n\t}")
	if err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile(f)
	want := "func main() {\n\t\tif true {\n\t\t\tfmt.Println(\"bye\")\n\t\t}\n}\n"
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestApply_TrailingWhitespace(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "test.txt")
	os.WriteFile(f, []byte("line one  \nline two\nline three\n"), 0644)

	// Search without trailing spaces
	err := Apply(f, "line one\nline two", "line one\nline TWO")
	if err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile(f)
	if want := "line one\nline TWO\nline three\n"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestApply_EmptySearch_Append(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "test.txt")
	os.WriteFile(f, []byte("existing\n"), 0644)

	err := Apply(f, "", "appended\n")
	if err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile(f)
	if want := "existing\nappended\n"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestApply_EmptySearch_CreateFile(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "subdir", "new.txt")

	err := Apply(f, "", "new content\n")
	if err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile(f)
	if want := "new content\n"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestApply_EmptyReplace_Delete(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "test.txt")
	os.WriteFile(f, []byte("keep\ndelete me\nkeep too\n"), 0644)

	err := Apply(f, "delete me\n", "")
	if err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile(f)
	if want := "keep\nkeep too\n"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestApply_NotFound(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "test.txt")
	os.WriteFile(f, []byte("hello world\n"), 0644)

	err := Apply(f, "nonexistent content", "replacement")
	if err == nil {
		t.Fatal("expected error for not-found search")
	}
}

func TestApply_MultipleMatches_OnlyFirst(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "test.txt")
	os.WriteFile(f, []byte("AAA\nBBB\nAAA\nCCC\n"), 0644)

	err := Apply(f, "AAA", "ZZZ")
	if err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile(f)
	if want := "ZZZ\nBBB\nAAA\nCCC\n"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestApply_LargeIndentOffset(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "test.py")
	// File has 8-space indent
	os.WriteFile(f, []byte("class Foo:\n        def bar(self):\n            return 1\n"), 0644)

	// Search has 0-space indent
	err := Apply(f, "def bar(self):\n    return 1", "def bar(self):\n    return 2")
	if err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile(f)
	want := "class Foo:\n        def bar(self):\n            return 2\n"
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestApply_FileNotExist_NonEmptySearch(t *testing.T) {
	err := Apply("/tmp/nonexistent_ax_test_file.go", "some content", "replacement")
	if err == nil {
		t.Fatal("expected error for non-existent file with non-empty search")
	}
}
