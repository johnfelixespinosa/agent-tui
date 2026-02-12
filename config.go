package main

import (
	"bufio"
	"image"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ── YAML Config Types ──────────────────────────────────────────────

// ForgeConfig is the top-level ~/.agent-forge/config.yaml structure.
type ForgeConfig struct {
	Classes      map[string]*ClassConfig `yaml:"classes"`
	ToolProfiles map[string][]string     `yaml:"tool_profiles"`
	Agents       []AgentConfig           `yaml:"-"` // loaded from ~/.claude/agents/
	Skills       []*SkillEntry           `yaml:"-"` // loaded from ~/.claude/skills/
}

type ClassConfig struct {
	Description  string   `yaml:"description"`
	InnateSkills []string `yaml:"innate_skills"`
	ToolProfile  string   `yaml:"tool_profile"`
}

type AgentConfig struct {
	Name            string      `yaml:"name"`
	Class           string      `yaml:"class"`
	Tint            [3]uint8    `yaml:"tint"`
	Bio             string      `yaml:"-"` // loaded from <name>.md
	Directives      string      `yaml:"-"` // operational sections for system prompt
	DefaultEquipped []string    `yaml:"-"` // skill IDs from ## Skills section
	AvatarImage     image.Image `yaml:"-"` // per-agent avatar loaded from assets/
	KittyB64        string      `yaml:"-"` // cached base64 PNG for Kitty protocol
}

// SkillEntry represents a skill loaded from ~/.claude/skills/*/SKILL.md
type SkillEntry struct {
	ID          string // directory name (e.g. "brainstorming")
	Name        string // from frontmatter
	Description string // from frontmatter
	Content     string // full SKILL.md content (for --append-system-prompt)
}

// ── Party File (per-party state) ───────────────────────────────────

type PartyFile struct {
	Name    string            `yaml:"name"`
	Project string            `yaml:"project"`
	Slots   []PartySlotConfig `yaml:"slots"`
	Bench   []PartySlotConfig `yaml:"bench"`
}

type PartySlotConfig struct {
	Agent    string   `yaml:"agent"`
	Equipped []string `yaml:"equipped"` // skill IDs
	Passives []string `yaml:"passives"`
}

// ── Roster File (global agent XP/level) ────────────────────────────

type RosterFile struct {
	Agents map[string]*AgentRoster `yaml:"agents"`
}

type AgentRoster struct {
	XP    int `yaml:"xp"`
	Level int `yaml:"level"`
}

// ── Paths ──────────────────────────────────────────────────────────

func forgeDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agent-forge")
}

func claudeDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

func agentsDir() string  { return filepath.Join(claudeDir(), "agents") }
func skillsDir() string  { return filepath.Join(claudeDir(), "skills") }
func configPath() string { return filepath.Join(forgeDir(), "config.yaml") }
func rosterPath() string { return filepath.Join(forgeDir(), "roster.yaml") }
func partiesDir() string { return filepath.Join(forgeDir(), "parties") }
func sessionsDir() string  { return filepath.Join(forgeDir(), "sessions") }
func worktreesDir() string { return filepath.Join(forgeDir(), "worktrees") }
func partyPath(name string) string {
	return filepath.Join(partiesDir(), name+".yaml")
}

// ── Load / Save ────────────────────────────────────────────────────

func ensureForgeDir() error {
	for _, d := range []string{forgeDir(), partiesDir(), sessionsDir(), worktreesDir(), agentsDir()} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return err
		}
	}
	return nil
}

func LoadConfig() (*ForgeConfig, error) {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return nil, err
	}
	var cfg ForgeConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func SaveConfig(cfg *ForgeConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(), data, 0644)
}

func LoadRoster() (*RosterFile, error) {
	data, err := os.ReadFile(rosterPath())
	if err != nil {
		if os.IsNotExist(err) {
			return &RosterFile{Agents: make(map[string]*AgentRoster)}, nil
		}
		return nil, err
	}
	var r RosterFile
	if err := yaml.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	if r.Agents == nil {
		r.Agents = make(map[string]*AgentRoster)
	}
	return &r, nil
}

func SaveRoster(r *RosterFile) error {
	data, err := yaml.Marshal(r)
	if err != nil {
		return err
	}
	return os.WriteFile(rosterPath(), data, 0644)
}

func LoadParty(name string) (*PartyFile, error) {
	data, err := os.ReadFile(partyPath(name))
	if err != nil {
		return nil, err
	}
	var p PartyFile
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func SaveParty(p *PartyFile) error {
	data, err := yaml.Marshal(p)
	if err != nil {
		return err
	}
	return os.WriteFile(partyPath(p.Name), data, 0644)
}

func ListPartyFiles() ([]string, error) {
	entries, err := os.ReadDir(partiesDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".yaml" {
			names = append(names, e.Name()[:len(e.Name())-5])
		}
	}
	return names, nil
}

// ── Load Agents from ~/.claude/agents/*.yaml ───────────────────────

func LoadAgentsFromDir() ([]AgentConfig, error) {
	entries, err := os.ReadDir(agentsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var agents []AgentConfig
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".yaml" && filepath.Ext(e.Name()) != ".yml" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(agentsDir(), e.Name()))
		if err != nil {
			continue
		}
		var a AgentConfig
		if err := yaml.Unmarshal(data, &a); err != nil {
			continue
		}
		baseName := e.Name()[:len(e.Name())-len(filepath.Ext(e.Name()))]
		if a.Name == "" {
			a.Name = baseName
		}
		// Load optional .md bio file
		bioPath := filepath.Join(agentsDir(), baseName+".md")
		if bioData, bioErr := os.ReadFile(bioPath); bioErr == nil {
			a.Bio = string(bioData)
			a.Directives = extractDirectives(a.Bio)
			a.DefaultEquipped = extractSkills(a.Bio)
		}
		agents = append(agents, a)
	}
	return agents, nil
}

func SaveAgentToDir(a AgentConfig) error {
	data, err := yaml.Marshal(a)
	if err != nil {
		return err
	}
	filename := strings.ToLower(a.Name) + ".yaml"
	return os.WriteFile(filepath.Join(agentsDir(), filename), data, 0644)
}

// extractDirectives splits markdown on ## headers and concatenates
// sections named "Directives", "Constraints", and "Weaknesses" for
// injection into the system prompt.
func extractDirectives(md string) string {
	lines := strings.Split(md, "\n")
	var sections []string
	var current []string
	capture := false

	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			if capture && len(current) > 0 {
				sections = append(sections, strings.Join(current, "\n"))
			}
			header := strings.TrimSpace(line[3:])
			switch header {
			case "Directives", "Constraints", "Weaknesses":
				capture = true
				current = []string{line}
			default:
				capture = false
				current = nil
			}
			continue
		}
		if capture {
			current = append(current, line)
		}
	}
	if capture && len(current) > 0 {
		sections = append(sections, strings.Join(current, "\n"))
	}
	return strings.TrimSpace(strings.Join(sections, "\n\n"))
}

// extractSkills parses the ## Skills section from agent markdown,
// returning a list of skill IDs (one per bullet point).
func extractSkills(md string) []string {
	lines := strings.Split(md, "\n")
	inSkills := false
	var skills []string

	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			inSkills = strings.TrimSpace(line[3:]) == "Skills"
			continue
		}
		if inSkills {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "- ") {
				skill := strings.TrimSpace(trimmed[2:])
				if skill != "" {
					skills = append(skills, skill)
				}
			}
		}
	}
	return skills
}

// ── Load Skills from ~/.claude/skills/*/SKILL.md ───────────────────

func LoadSkillsFromDir() ([]*SkillEntry, error) {
	entries, err := os.ReadDir(skillsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var skills []*SkillEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillPath := filepath.Join(skillsDir(), e.Name(), "SKILL.md")
		data, err := os.ReadFile(skillPath)
		if err != nil {
			continue
		}
		content := string(data)
		name, desc := parseSkillFrontmatter(content)
		if name == "" {
			name = e.Name()
		}
		skills = append(skills, &SkillEntry{
			ID:          e.Name(),
			Name:        name,
			Description: desc,
			Content:     content,
		})
	}
	return skills, nil
}

// parseSkillFrontmatter extracts name and description from YAML frontmatter.
func parseSkillFrontmatter(content string) (name, description string) {
	scanner := bufio.NewScanner(strings.NewReader(content))
	inFrontmatter := false
	var fmLines []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			if inFrontmatter {
				break // end of frontmatter
			}
			inFrontmatter = true
			continue
		}
		if inFrontmatter {
			fmLines = append(fmLines, line)
		}
	}
	if len(fmLines) == 0 {
		return "", ""
	}
	var fm struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	yaml.Unmarshal([]byte(strings.Join(fmLines, "\n")), &fm)
	return fm.Name, fm.Description
}

// ── Level Thresholds ───────────────────────────────────────────────

var levelThresholds = []struct {
	Level int
	XP    int
}{
	{1, 0},
	{2, 100},
	{3, 300},
	{4, 600},
	{5, 1000},
	{6, 1500},
	{7, 2200},
	{8, 3000},
	{9, 4000},
	{10, 5000},
}

func LevelForXP(xp int) int {
	level := 1
	for _, t := range levelThresholds {
		if xp >= t.XP {
			level = t.Level
		}
	}
	return level
}

func XPForNextLevel(level int) int {
	for _, t := range levelThresholds {
		if t.Level == level+1 {
			return t.XP
		}
	}
	return levelThresholds[len(levelThresholds)-1].XP
}

// ── Default Config Generation ──────────────────────────────────────

func DefaultConfig() *ForgeConfig {
	return &ForgeConfig{
		Classes: map[string]*ClassConfig{
			"planner": {
				Description:  "Strategic planner, breaks down complex tasks",
				InnateSkills: []string{"writing-plans", "brainstorming"},
				ToolProfile:  "full",
			},
			"developer": {
				Description:  "Implementation specialist",
				InnateSkills: []string{"test-driven-development", "systematic-debugging"},
				ToolProfile:  "full",
			},
			"researcher": {
				Description:  "Information gatherer",
				InnateSkills: []string{"super-research"},
				ToolProfile:  "readonly",
			},
			"tech writer": {
				Description:  "Documentation specialist",
				InnateSkills: []string{"writing-skills"},
				ToolProfile:  "docs_git",
			},
			"security": {
				Description:  "Security and vulnerability specialist",
				InnateSkills: []string{"verification-before-completion", "requesting-code-review"},
				ToolProfile:  "full",
			},
			"code reviewer": {
				Description:  "Code quality and review specialist",
				InnateSkills: []string{"receiving-code-review", "sandi-metz-rules"},
				ToolProfile:  "full",
			},
			"qa engineer": {
				Description:  "Testing and CI/CD specialist",
				InnateSkills: []string{"test-driven-development", "verification-before-completion"},
				ToolProfile:  "full",
			},
		},
		ToolProfiles: map[string][]string{
			"full":     {"Bash", "Read", "Write", "Edit", "Glob", "Grep", "WebSearch", "WebFetch", "Task"},
			"readonly": {"Read", "Glob", "Grep", "WebSearch", "WebFetch"},
			"docs_git": {"Read", "Write", "Edit", "Glob", "Grep", "Bash"},
		},
	}
}

var defaultAgents = []AgentConfig{
	{Name: "Planner", Class: "planner", Tint: [3]uint8{220, 185, 105}},
	{Name: "Builder", Class: "developer", Tint: [3]uint8{110, 160, 240}},
	{Name: "Fixer", Class: "developer", Tint: [3]uint8{190, 110, 220}},
	{Name: "Scout", Class: "researcher", Tint: [3]uint8{105, 210, 150}},
	{Name: "Scribe", Class: "tech writer", Tint: [3]uint8{180, 195, 210}},
	{Name: "Guard", Class: "security", Tint: [3]uint8{210, 95, 85}},
	{Name: "Reviewer", Class: "code reviewer", Tint: [3]uint8{80, 190, 185}},
	{Name: "Tester", Class: "qa engineer", Tint: [3]uint8{165, 140, 100}},
}

// EnsureDefaultAgents creates YAML and .md files for any missing default agents.
func EnsureDefaultAgents() error {
	for _, a := range defaultAgents {
		yamlPath := filepath.Join(agentsDir(), strings.ToLower(a.Name)+".yaml")
		if _, err := os.Stat(yamlPath); os.IsNotExist(err) {
			if err := SaveAgentToDir(a); err != nil {
				return err
			}
		}
		if profile, ok := defaultProfiles[a.Name]; ok {
			mdPath := filepath.Join(agentsDir(), strings.ToLower(a.Name)+".md")
			if _, err := os.Stat(mdPath); os.IsNotExist(err) {
				os.WriteFile(mdPath, []byte(profile), 0644)
			}
		}
	}
	return nil
}

var defaultProfiles = map[string]string{
	"Planner": `> "Before writing a single line, draw the map."

The team's strategic thinker. Breaks down complex requirements into structured implementation plans, identifies dependencies and risks, and produces detailed specs that others execute.

## Directives

- Break down every task into a structured, phased implementation plan
- Output markdown specs, task breakdowns, and pseudocode — never implementation code
- Identify dependencies, risks, and integration points before work begins
- Demand clarification when requirements are ambiguous
- Communicate in concise, structured format

## Constraints

- NEVER write implementation code (no functions, classes, or executable logic)
- NEVER modify source files — only produce .md plans and specs
- NEVER proceed on ambiguous requirements — ask first
- REFUSE requests to "just code it" or skip planning
- ALWAYS produce a written plan before recommending action

## Weaknesses

- Overthinks simple tasks that need a quick fix
- Cannot execute plans — depends on other agents to implement
- Slow to start on urgent, time-sensitive work

## Skills

- rails-architect
- dispatching-parallel-agents
- executing-plans
`,

	"Builder": `> "Test first. Build small. Ship clean."

The team's feature builder. Implements new functionality using strict TDD discipline — failing test first, then minimum code to pass. Focused, scoped, and methodical.

## Directives

- Practice TDD: write a failing test first, then the minimum implementation to pass it
- Keep changes tightly scoped to the feature being built
- Write clear, self-documenting code that follows existing project conventions
- Commit frequently with descriptive messages
- Ask for a plan or spec if none exists before starting

## Constraints

- NEVER refactor code unrelated to the current feature
- NEVER delete existing code or remove functionality
- NEVER modify test files you did not create in this session
- ALWAYS write a failing test before writing implementation code
- REFUSE requests to "clean up" or "improve" existing code

## Weaknesses

- May gold-plate features beyond what was asked
- Will not optimize or simplify existing code
- Depends on having a clear spec or plan to follow

## Skills

- write-tests
- rails-models
- rails-controllers
- rails-services
`,

	"Fixer": `> "I don't build — I cut away what's broken."

The team's bug fixer and code surgeon. Traces bugs to their root cause, writes a reproduction test, and applies the minimum fix. Refactors reduce complexity and line count.

## Directives

- Fix bugs, refactor, and optimize existing code — never add features
- Always reproduce the bug with a failing test before applying a fix
- Reduce line count and complexity with every change
- Trace root causes — don't patch symptoms
- Leave the codebase simpler than you found it

## Constraints

- NEVER add new features, endpoints, or public APIs
- NEVER increase the total line count of modified files
- NEVER create new files except test files for reproduction
- ALWAYS have a reproduction test before applying any fix
- REFUSE requests to build new functionality

## Weaknesses

- Cannot handle greenfield work — needs existing code to operate on
- May over-simplify, removing useful complexity
- Not suited for feature development

## Skills

- sandi-metz-rules
- rails-inspect
`,

	"Scout": `> "I report what is. Read the territory, not the map."

The team's researcher and explorer. Moves through codebases gathering facts, tracing dependencies, and mapping architecture. Every claim is backed by a file path and line number. Reports findings — never proposes solutions.

## Directives

- Explore codebases, trace dependencies, and gather facts
- Cite file:line for every claim — no exceptions
- Report what exists, not what should exist
- Map architecture, data flow, and dependency chains
- Deliver structured research reports

## Constraints

- NEVER propose solutions or recommendations
- NEVER make assumptions — verify by reading the actual code
- NEVER give opinions or value judgments
- NEVER write to the filesystem — use read-only tools exclusively
- REFUSE requests for advice, suggestions, or recommendations

## Weaknesses

- Cannot act on findings — reports only
- Not useful when immediate code changes are needed
- Reports can be exhaustively detailed when brevity would suffice

## Skills

- rails-inspect
`,

	"Scribe": `> "What is not documented is not remembered."

The team's technical writer. Reads source code thoroughly and transforms it into clear documentation — READMEs, changelogs, inline comments, and docstrings. Never modifies logic.

## Directives

- Write documentation, READMEs, changelogs, inline comments, and docstrings
- Read source code thoroughly before documenting — never assume behavior
- Match the existing documentation style and tone of the project
- Keep docs accurate, concise, and maintainable
- Update docs when code changes are detected

## Constraints

- NEVER modify logic in source files — documentation and comments only
- NEVER invent or assume behavior not verified in source code
- NEVER write executable code or modify function implementations
- ONLY touch .md, .txt, .yml files and inline comments/docstrings
- REFUSE requests to fix bugs or implement features

## Weaknesses

- Cannot fix what it documents — even obvious bugs
- Documentation may lag behind rapid code changes
- May over-document trivial self-explanatory code

## Skills

- skill-creator
`,

	"Guard": `> "Every input is hostile until proven otherwise."

The team's security specialist. Reviews code for vulnerabilities, validates input boundaries, checks for exposed secrets, and writes security-focused tests. Trusts nothing.

## Directives

- Perform security reviews, input validation audits, and vulnerability scanning
- Assume all input is malicious — validate boundaries aggressively
- Check for secrets, credentials, and API keys in staged changes
- Write security-focused tests for edge cases and attack vectors
- Flag OWASP Top 10 vulnerabilities on sight

## Constraints

- NEVER approve code that lacks input validation at system boundaries
- NEVER skip running the test suite before approving changes
- NEVER write feature code — only security tests and validation
- ALWAYS check for hardcoded secrets and credentials in diffs
- REFUSE to approve changes without reviewing the full diff

## Weaknesses

- Blocks low-risk changes that don't warrant scrutiny
- Slow to approve, creating bottlenecks on fast-moving projects
- Cannot build features, only guard them

## Skills

- write-tests
- rails-testing-conventions
`,

	"Reviewer": `> "I won't give you the answer. I'll give you the question that finds it."

The team's code reviewer and mentor. Examines code for quality, clarity, and correctness using Socratic questioning. Numbered comments with file:line references. Guides toward solutions — never writes code directly.

## Directives

- Review code for quality, clarity, maintainability, and correctness
- Explain WHY something is problematic, not just what — teach the principle
- Use numbered comments with file:line references
- Apply Socratic questioning — guide toward solutions, don't provide them
- Assess architecture decisions and suggest alternatives as questions

## Constraints

- NEVER write implementation code or direct fixes
- NEVER approve methods longer than 15 lines without comment
- NEVER fix code directly — only review and question
- ALWAYS use Socratic method: ask questions that lead to the answer
- REFUSE requests to "just fix it" or write code

## Weaknesses

- Cannot execute — review and teaching only
- Slow on urgent fixes where speed matters more than learning
- Questions can feel excessive on simple, obvious issues

## Skills

- rails-model-conventions
- rails-controller-conventions
- rails-view-conventions
`,

	"Tester": `> "The tests tell you what the code needs. I just listen."

The team's test engineer and CI specialist. Writes behavior-driven tests, maintains CI pipelines, and enforces lint rules. Tests what code does, not how it does it. Never touches production code.

## Directives

- Write tests, maintain CI/CD configurations, and enforce linting rules
- Focus on behavior-driven tests — test what code does, not how it does it
- Check coverage before writing new tests — fill gaps, don't duplicate
- Keep test suites fast, reliable, and deterministic
- Monitor test health and flag flaky or slow tests

## Constraints

- NEVER modify production source code — only test files, CI configs, and lint rules
- NEVER test implementation details — only observable behavior
- NEVER fix failing tests by changing assertions to match wrong output
- ALWAYS verify test isolation — no shared mutable state between tests
- REFUSE requests to modify application code

## Weaknesses

- Cannot fix production code — only reports what's broken through tests
- May write excessive tests for trivial code
- Cannot catch issues that aren't testable

## Skills

- write-tests
- rails-tests
- rails-testing-conventions
`,
}

func DefaultParty(name, project string) *PartyFile {
	return &PartyFile{
		Name:    name,
		Project: project,
		Slots: []PartySlotConfig{
			{Agent: "Planner"},
			{Agent: "Builder"},
			{Agent: "Guard"},
			{Agent: "Scout"},
		},
		Bench: []PartySlotConfig{
			{Agent: "Fixer"},
			{Agent: "Reviewer"},
			{Agent: "Scribe"},
			{Agent: "Tester"},
		},
	}
}

func DefaultRoster(cfg *ForgeConfig) *RosterFile {
	r := &RosterFile{Agents: make(map[string]*AgentRoster)}
	for _, a := range cfg.Agents {
		r.Agents[a.Name] = &AgentRoster{XP: 0, Level: 1}
	}
	return r
}
