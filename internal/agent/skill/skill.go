package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Skill represents a parsed SKILL.md file.
type Skill struct {
	Name        string // from frontmatter, falls back to directory name
	Description string // from frontmatter
	AlwaysLoad  bool   // if true, body injected into system prompt
	Body        string // markdown after YAML frontmatter
}

// frontmatter is the YAML metadata block in SKILL.md.
type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	AlwaysLoad  bool   `yaml:"always_load"`
}

// Loader manages skills loaded from file system.
type Loader struct {
	skills map[string]*Skill // name → skill (project overrides global)
}

// NewLoader scans globalDir then projectDir for SKILL.md files.
// Project skills override global skills with the same name.
// Missing directories are silently skipped.
func NewLoader(globalDir, projectDir string) (*Loader, error) {
	l := &Loader{skills: make(map[string]*Skill)}
	if err := l.loadDir(globalDir); err != nil {
		return nil, err
	}
	if err := l.loadDir(projectDir); err != nil {
		return nil, err
	}
	return l, nil
}

// loadDir scans a single skills directory for <name>/SKILL.md files.
func (l *Loader) loadDir(dir string) error {
	if dir == "" {
		return nil
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillPath := filepath.Join(dir, entry.Name(), "SKILL.md")
		data, err := os.ReadFile(skillPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue // directory without SKILL.md is ok
			}
			return err
		}
		skill, err := parseSkill(entry.Name(), data)
		if err != nil {
			return fmt.Errorf("skill %s: %w", entry.Name(), err)
		}
		l.skills[skill.Name] = skill // project overwrites global
	}
	return nil
}

// parseSkill parses a SKILL.md file content.
// dirName is used as fallback when frontmatter name is empty.
func parseSkill(dirName string, data []byte) (*Skill, error) {
	content := strings.TrimSpace(string(data))
	fm, body, err := splitFrontmatter(content)
	if err != nil {
		return nil, err
	}
	name := fm.Name
	if name == "" {
		name = dirName
	}
	if fm.Description == "" {
		return nil, fmt.Errorf("missing description")
	}
	return &Skill{
		Name:        name,
		Description: fm.Description,
		AlwaysLoad:  fm.AlwaysLoad,
		Body:        strings.TrimSpace(body),
	}, nil
}

// splitFrontmatter extracts YAML between the first pair of --- lines.
func splitFrontmatter(content string) (frontmatter, string, error) {
	if !strings.HasPrefix(content, "---") {
		return frontmatter{}, "", fmt.Errorf("frontmatter must start with ---")
	}
	rest := content[3:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return frontmatter{}, "", fmt.Errorf("frontmatter must end with ---")
	}
	yamlBlock := rest[:idx]
	body := rest[idx+4:] // skip "\n---"
	var fm frontmatter
	if err := yaml.Unmarshal([]byte(yamlBlock), &fm); err != nil {
		return frontmatter{}, "", fmt.Errorf("invalid YAML: %w", err)
	}
	return fm, body, nil
}

// Descriptions returns a sorted list of "  - name: description" lines
// suitable for inclusion in the system prompt.
func (l *Loader) Descriptions() string {
	names := make([]string, 0, len(l.skills))
	for name := range l.skills {
		names = append(names, name)
	}
	sort.Strings(names)
	var lines []string
	for _, name := range names {
		s := l.skills[name]
		lines = append(lines, fmt.Sprintf("  - %s: %s", s.Name, s.Description))
	}
	return strings.Join(lines, "\n")
}

// AlwaysLoaded returns always_load=true skills sorted by name.
func (l *Loader) AlwaysLoaded() []*Skill {
	var result []*Skill
	for _, s := range l.skills {
		if s.AlwaysLoad {
			result = append(result, s)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// Get returns a skill's full body wrapped in <skill> tags, or an error
// if the skill is not found.
func (l *Loader) Get(name string) (string, error) {
	s, ok := l.skills[name]
	if !ok {
		return "", fmt.Errorf("unknown skill '%s'", name)
	}
	return fmt.Sprintf("<skill name=\"%s\">\n%s\n</skill>", s.Name, s.Body), nil
}
