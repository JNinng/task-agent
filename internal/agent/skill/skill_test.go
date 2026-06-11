package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTempSkill(t *testing.T, dir, skillDir, content string) string {
	t.Helper()
	skillPath := filepath.Join(dir, skillDir)
	if err := os.MkdirAll(skillPath, 0755); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(skillPath, "SKILL.md")
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return filePath
}

func TestParseSkill_Valid(t *testing.T) {
	content := "---\nname: code-review\ndescription: Review code for correctness\nalways_load: false\n---\n# Code Review\n\nStep 1: Read the diff."

	s, err := parseSkill("code-review", []byte(content))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "code-review" {
		t.Errorf("name = %q, want %q", s.Name, "code-review")
	}
	if s.Description != "Review code for correctness" {
		t.Errorf("description = %q", s.Description)
	}
	if s.AlwaysLoad {
		t.Error("always_load should be false")
	}
	if s.Body != "# Code Review\n\nStep 1: Read the diff." {
		t.Errorf("body = %q", s.Body)
	}
}

func TestParseSkill_FallsBackToDirName(t *testing.T) {
	content := "---\ndescription: Some skill\n---\n\nBody text."

	s, err := parseSkill("my-skill", []byte(content))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "my-skill" {
		t.Errorf("name = %q, want %q", s.Name, "my-skill")
	}
}

func TestParseSkill_MissingDescription(t *testing.T) {
	content := "---\nname: bad\n---\n\nBody."

	_, err := parseSkill("bad", []byte(content))
	if err == nil {
		t.Fatal("expected error for missing description")
	}
}

func TestParseSkill_AlwaysLoad(t *testing.T) {
	content := "---\nname: auto\ndescription: Auto-loaded skill\nalways_load: true\n---\n\nAlways present."

	s, err := parseSkill("auto", []byte(content))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !s.AlwaysLoad {
		t.Error("always_load should be true")
	}
}

func TestParseSkill_NoFrontmatter(t *testing.T) {
	content := "# Just a heading\nNo frontmatter here."

	_, err := parseSkill("test", []byte(content))
	if err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
}

func TestParseSkill_CRLFFile(t *testing.T) {
	content := "---\r\nname: crlf\r\ndescription: Windows line endings\r\n---\r\n\r\nBody with CRLF.\r\n"

	s, err := parseSkill("crlf", []byte(content))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "crlf" {
		t.Errorf("name = %q, want %q", s.Name, "crlf")
	}
	if s.Description != "Windows line endings" {
		t.Errorf("description = %q", s.Description)
	}
	if s.Body != "Body with CRLF." {
		t.Errorf("body = %q, want %q", s.Body, "Body with CRLF.")
	}
}

func TestParseSkill_EmptyBody(t *testing.T) {
	content := "---\nname: minimal\ndescription: Just metadata\n---\n\n"

	s, err := parseSkill("minimal", []byte(content))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Body != "" {
		t.Errorf("body = %q, want empty", s.Body)
	}
}

func TestNewLoader_EmptyDirs(t *testing.T) {
	dir := t.TempDir()
	l, err := NewLoader(dir, filepath.Join(dir, "project"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if desc := l.Descriptions(); desc != "" {
		t.Errorf("descriptions = %q, want empty", desc)
	}
}

func TestNewLoader_LoadsSkills(t *testing.T) {
	globalDir := t.TempDir()
	writeTempSkill(t, globalDir, "git", "---\nname: git\ndescription: Git workflow helpers\n---\n\n# Git Skill\n")

	l, err := NewLoader(globalDir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, err := l.Get("git")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if content != "<skill name=\"git\">\n# Git Skill\n</skill>" {
		t.Errorf("content = %q", content)
	}
}

func TestNewLoader_ProjectOverridesGlobal(t *testing.T) {
	globalDir := t.TempDir()
	writeTempSkill(t, globalDir, "review", "---\nname: review\ndescription: Global review\n---\n\nGlobal instructions.")

	projectDir := t.TempDir()
	writeTempSkill(t, projectDir, "review", "---\nname: review\ndescription: Project review\n---\n\nProject instructions.")

	l, err := NewLoader(globalDir, projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, err := l.Get("review")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if content != "<skill name=\"review\">\nProject instructions.\n</skill>" {
		t.Errorf("content = %q, want project version", content)
	}
}

func TestLoader_Descriptions_Sorted(t *testing.T) {
	dir := t.TempDir()
	writeTempSkill(t, dir, "zzz", "---\nname: zzz\ndescription: Last\n---\n\nZ.")
	writeTempSkill(t, dir, "aaa", "---\nname: aaa\ndescription: First\n---\n\nA.")

	l, err := NewLoader(dir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	desc := l.Descriptions()
	expected := "  - aaa: First\n  - zzz: Last"
	if desc != expected {
		t.Errorf("descriptions = %q, want %q", desc, expected)
	}
}

func TestLoader_AlwaysLoaded(t *testing.T) {
	dir := t.TempDir()
	writeTempSkill(t, dir, "always", "---\nname: always\ndescription: Always loaded\nalways_load: true\n---\n\nAlways here.")
	writeTempSkill(t, dir, "manual", "---\nname: manual\ndescription: Manual load\nalways_load: false\n---\n\nLoad me manually.")

	l, err := NewLoader(dir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	loaded := l.AlwaysLoaded()
	if len(loaded) != 1 {
		t.Fatalf("len(always_loaded) = %d, want 1", len(loaded))
	}
	if loaded[0].Name != "always" {
		t.Errorf("always_loaded[0].Name = %q, want %q", loaded[0].Name, "always")
	}
}

func TestLoader_Get_Unknown(t *testing.T) {
	dir := t.TempDir()
	l, err := NewLoader(dir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = l.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown skill")
	}
}

func TestLoader_DirNotFound(t *testing.T) {
	l, err := NewLoader("/nonexistent/path/12345", "")
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}
	if len(l.skills) != 0 {
		t.Errorf("skills = %d, want 0", len(l.skills))
	}
}
