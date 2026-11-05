# Third-party notices

`piste` contains logic ported (TypeScript → Go) from the projects below. They
are not dependencies: nothing is fetched, linked or executed from them at
build or run time. `piste --licenses` prints this file.

## rulesync

- **Project:** https://github.com/dyoshikawa/rulesync
- **Version ported:** v22.0.0, commit `81b97709` (`81b9770933f37dc379072f1aed7b204082d99208`)
- **License:** MIT

```
MIT License

Copyright (c) 2024 dyoshikawa

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

### Ported files

Every Go file below carries a `// Portions ported from rulesync v22.0.0` header.
CI checks that this table and those headers agree (`cli/tools/check-notices`),
and `cli/tools/upstream-watch` lists upstream commits to the paths in the
second column since `81b97709`.

<!-- ported-files:begin -->
| piste file (under `cli/`) | rulesync paths at v22.0.0 | What was taken |
|---|---|---|
| `internal/agents/adapter/instructions.go` | `src/features/rules/claudecode-rule.ts`, `src/features/rules/codexcli-rule.ts`, `src/features/rules/agentsmd-rule.ts`, `src/features/rules/hermesagent-rule.ts`, `src/features/rules/rules-processor.ts`, `src/constants/codexcli-paths.ts` | root vs nested placement, the Codex 32 KiB `project_doc_max_bytes` fold, no Hermes global rules |
| `internal/agents/adapter/skills.go` | `src/features/skills/claudecode-skill.ts`, `src/features/skills/codexcli-skill.ts`, `src/features/skills/hermesagent-skill.ts`, `src/features/skills/agentsmd-skill.ts`, `src/constants/claudecode-paths.ts`, `src/constants/codexcli-paths.ts`, `src/constants/agentsmd-paths.ts`, `src/constants/hermesagent-paths.ts` | per-harness skill paths (Codex → `.agents/skills`), Claude-only frontmatter keys, Hermes category nesting |
| `internal/agents/adapter/subagents.go` | `src/features/subagents/claudecode-subagent.ts`, `src/features/subagents/claudecode-plugin-subagent.ts`, `src/features/subagents/codexcli-subagent.ts` | Claude `.md` → Codex `.toml` mapping, the `'''` rule, plugin-agent field drops |
| `internal/agents/adapter/hooks.go` | `src/types/hooks.ts`, `src/features/hooks/tool-hooks-converter.ts`, `src/features/hooks/claudecode-hooks.ts`, `src/features/hooks/claudecode-plugin-hooks.ts`, `src/features/hooks/codexcli-hooks.ts`, `src/features/hooks/hermesagent-hooks.ts` | canonical ↔ Claude / Codex / Hermes event tables, no-matcher events, the `"$CLAUDE_PLUGIN_ROOT"` rewrite |
| `internal/agents/adapter/mcp.go` | `src/features/mcp/claudecode-mcp.ts`, `src/features/mcp/codexcli-mcp.ts`, `src/features/mcp/hermesagent-mcp.ts`, `src/features/mcp/rulesync-mcp.ts` | field mapping; Codex `bearer_token_env_var` / `env_http_headers` / `env_vars` / `startup_timeout_sec` |
| `internal/agents/adapter/permissions.go` | `src/features/permissions/codexcli-permissions.ts`, `src/features/permissions/claudecode-permissions.ts` | the `.rules` / `prefix_rule` format and decision mapping (with the trailing-`*` fix) |
| `internal/agents/adapter/hermes.go` | `src/utils/hermesagent.ts`, `src/constants/hermesagent-paths.ts` | Hermes home layout |
| `internal/agents/adapter/adapter_test.go` | `src/features/permissions/codexcli-permissions.test.ts`, `src/features/hooks/claudecode-plugin-hooks.test.ts`, `src/features/subagents/codexcli-subagent.test.ts`, `src/features/mcp/codexcli-mcp.test.ts` | transcribed cases |
| `internal/agents/carrier/carrier.go` | `src/constants/plugin-paths.ts`, `src/utils/plugin-root.ts`, `src/features/hooks/claudecode-plugin-hooks.ts`, `src/features/subagents/claudecode-plugin-subagent.ts` | the plugin directory layout |
| `internal/agents/safety/safety.go` | `src/utils/control-characters.ts`, `src/utils/confusable-names.ts` | control / invisible character rules, mixed-script and lookalike detection |
| `internal/agents/safety/safety_test.go` | `src/utils/control-characters.test.ts`, `src/utils/confusable-names.test.ts` | transcribed cases |
| `internal/agents/lock/lock.go` | `src/lib/sources-lock.ts`, `src/lib/sources.ts` | lock fields and frozen/update semantics |
| `internal/agents/plan/plan.go` | `src/lib/orphan-sweep.ts`, `src/lib/shared-file-derive.ts` | the plan-then-apply removal structure |
| `internal/agents/edit/json.go` | `src/utils/content-equivalence.ts` | parse-then-compare equivalence |
<!-- ported-files:end -->

Not ported: the whole-section merge, `--delete` sweep semantics,
smol-toml / js-yaml re-serialisation, generated Hermes Python plugins, `fetch`
without a lock, self-update, and global-config discovery.

## furedea/agent-harness

The rule "replace harness-owned keys, preserve tool-owned keys" is borrowed as
an idea. No code is ported.
