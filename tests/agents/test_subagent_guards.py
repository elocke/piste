"""Tests for the inline Bash guard hooks in agents/agents/*.md frontmatter
(implementer). The guards are accident guards (denylists), not boundaries.
Run: python3 -m unittest discover -s tests/agents -p 'test_subagent_guards.py'"""

import json
import pathlib
import re
import shutil
import subprocess
import unittest

AGENTS = pathlib.Path(__file__).resolve().parents[2] / "agents" / "agents"


def guard(name):
    """The single-quoted `command:` of the agent's Bash hook, YAML-unescaped."""
    front = (AGENTS / f"{name}.md").read_text().split("---", 2)[1]
    m = re.search(r"^\s*command: '(.*)'\s*$", front, re.M)
    assert m, f"{name}: no single-quoted hook command"
    return m.group(1).replace("''", "'")


def blocked(name, cmd):
    event = json.dumps({"tool_name": "Bash", "tool_input": {"command": cmd}})
    r = subprocess.run(["sh", "-c", guard(name)], input=event, capture_output=True, text=True)
    assert r.returncode in (0, 2), f"{name}: exit {r.returncode}: {r.stderr}"
    return r.returncode == 2


CASES = {
    "adversarial-reviewer": (
        [
            "git diff $(git merge-base origin/main HEAD)...HEAD",
            "git log --oneline -20",
            "uv run pytest tests/ -x 2>&1 | tail -50",
            "grep -rn 'password' src/ 2>/dev/null",
            "terraform validate",
            "kustomize build env/prod | grep -c kind",
            "npm test >/dev/null 2>&1; echo $?",
            "gh pr view 12 --json files",
            "nix run .#piste -- build --host h",
        ],
        [
            "echo x > file.txt",
            "cat a >> b",
            "printf x | tee out",
            "sed -i 's/a/b/' f",
            "rm -rf build",
            "mv a b",
            "git commit -m x",
            "git push",
            "git checkout -- src/",
            "git stash",
            "gh pr comment 1 -b hi",
            "gh api -X POST repos/o/r/issues",
            "terraform apply",
            "kubectl delete pod x",
            "perl -pi -e 's/a/b/' f",
            "git -C ../repo commit -am x",
            "uv run pytest > out.txt 2>&1",
            "git merge feat/x",
            "piste switch",
            "nix run .#piste -- switch",
        ],
    ),
    "implementer": (
        [
            "git status",
            "git add -A && git commit -m 'feat(x): y'",
            "git checkout -b feat/x",
            "uv run pytest tests/",
            "npm test",
            "terraform validate",
            "kubectl get pods",
            "nix run .#piste -- build --host h",
        ],
        [
            "git push",
            "git push origin feat/x",
            "git commit --no-verify -m x",
            "git reset --hard origin/main",
            "git checkout main",
            "gh pr create --fill",
            "gh pr merge 1",
            "terraform apply",
            "kubectl apply -f x.yaml",
            "helm upgrade x y",
            "piste switch",
            "nix run .#piste -- switch",
            "darwin-rebuild switch --flake .",
        ],
    ),
}


class Frontmatter(unittest.TestCase):
    def test_shared_agents_are_named_after_their_file(self):
        for path in AGENTS.glob("*.md"):
            front = path.read_text().split("---", 2)[1]
            with self.subTest(agent=path.stem):
                self.assertRegex(front, rf"(?m)^name: {re.escape(path.stem)}$")
                self.assertRegex(front, r"(?m)^description: \S")


@unittest.skipUnless(shutil.which("jq"), "jq not installed")
class Guards(unittest.TestCase):
    def test_guards(self):
        for name, (allow, deny) in CASES.items():
            for cmd in allow:
                with self.subTest(agent=name, allow=cmd):
                    self.assertFalse(blocked(name, cmd))
            for cmd in deny:
                with self.subTest(agent=name, deny=cmd):
                    self.assertTrue(blocked(name, cmd))


if __name__ == "__main__":
    unittest.main()
