package adapter

// ClaudeOnlySkillKeys are the SKILL.md frontmatter fields only Claude Code
// reads; other harnesses drop them (the registry reports them as lossy).
func ClaudeOnlySkillKeys() []string { return append([]string(nil), claudeOnlyKeys...) }
