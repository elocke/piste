# Options reference

- Which `piste.*` settings can my overlay set, and what does each default to?
- Which engine file declares an option, so I can read what it does?

Generated from the engine's modules by `nix build .#docs-options` (on
x86_64-linux); don't edit it by hand. A change to an option fails
`checks.docs-options` until the page is rebuilt: `nix build .#docs-options
&& install -m 644 result docs/options.md`. Set these in your overlay's host
files; [customising.md](customising.md) shows where. Any home-manager,
nix-darwin or NixOS option works there too.

## piste\.agents\.enable



Whether to enable agent skills + instructions from the piste agent zones\.



*Type:*
boolean



*Default:*

```nix
false
```



*Example:*

```nix
true
```

*Declared by:*
 - [modules/programs/agents\.nix](../modules/programs/agents.nix)


## piste\.agents\.claudeProfiles

Claude settings layers for ` claude --settings `, name -> a preset with
` kind = "claude-profile" `\. Written to ~/\.claude/profiles/\<name>\.json\.



*Type:*
attribute set of string



*Default:*

```nix
{ }
```



*Example:*

```nix
{
  lean = "lean";
}
```

*Declared by:*
 - [modules/programs/agents\.nix](../modules/programs/agents.nix)


## piste\.agents\.presets



Preset names this host receives, resolved across all sources\.



*Type:*
list of string



*Default:*

```nix
[
  "base"
]
```



*Example:*

```nix
[
  "base"
  "personal"
]
```

*Declared by:*
 - [modules/programs/agents\.nix](../modules/programs/agents.nix)


## piste\.agents\.registry\.enable



Build the registry of this host’s zones (` piste agents registry build `)
and link it at ~/\.local/share/piste-agents/registry, where ` piste agents search/show/add ` read it\. ` --workdir ` reads the zone
checkouts live instead\.



*Type:*
boolean



*Default:*

```nix
true
```

*Declared by:*
 - [modules/programs/agents\.nix](../modules/programs/agents.nix)


## piste\.agents\.sources



Agent zone roots\. ` store ` is read at eval time (structure); ` workdir `
is the live checkout the skill links point at\. Both are the same
` agents/ ` directory of one repo\.



*Type:*
attribute set of (submodule)



*Default:*

```nix
{ }
```

*Declared by:*
 - [modules/programs/agents\.nix](../modules/programs/agents.nix)


## piste\.agents\.sources\.\<name>\.capabilities



Whether this source provides capabilities\.toml\.



*Type:*
boolean



*Default:*

```nix
false
```

*Declared by:*
 - [modules/programs/agents\.nix](../modules/programs/agents.nix)


## piste\.agents\.sources\.\<name>\.name



Source identifier, recorded in the manifest\.



*Type:*
string



*Default:*

```nix
"‹name›"
```

*Declared by:*
 - [modules/programs/agents\.nix](../modules/programs/agents.nix)


## piste\.agents\.sources\.\<name>\.store



The agents/ directory as a store path\. Read at eval time\.



*Type:*
absolute path

*Declared by:*
 - [modules/programs/agents\.nix](../modules/programs/agents.nix)


## piste\.agents\.sources\.\<name>\.workdir



Absolute path to the same agents/ directory in the working checkout\. Link target only\.



*Type:*
string

*Declared by:*
 - [modules/programs/agents\.nix](../modules/programs/agents.nix)


## piste\.agents\.sources\.\<name>\.zone



Trust zone of this source: “shared” or the host’s zone\.



*Type:*
string matching the pattern ^\[a-z]\[a-z0-9-]\*$

*Declared by:*
 - [modules/programs/agents\.nix](../modules/programs/agents.nix)


## piste\.agents\.targets



Harnesses to deploy to on this host\.



*Type:*
list of string



*Default:*

```nix
[
  "claude"
  "codex"
]
```

*Declared by:*
 - [modules/programs/agents\.nix](../modules/programs/agents.nix)


## piste\.agents\.zone



This host’s trust zone: any name, e\.g\. “personal”, “work”, “agent”\.
Every source must be “shared” or this zone;
anything else fails evaluation\.



*Type:*
a zone name other than “shared” (lowercase letters, digits, dashes)



*Example:*

```nix
"work"
```

*Declared by:*
 - [modules/programs/agents\.nix](../modules/programs/agents.nix)


## piste\.checkout



The piste checkout on this machine\. Agent skill links, pisteup and nixconf point into it\.



*Type:*
string



*Default:*

```nix
"${config.home.homeDirectory}/piste"
```

*Declared by:*
 - [modules/programs/checkout\.nix](../modules/programs/checkout.nix)


## piste\.github\.id



The account’s numeric user id (` gh api user --jq .id `)\. With ` login `, the git email is ` <id>+<login>@users.noreply.github.com ` unless ` programs.git.settings.user.email ` is set\.



*Type:*
null or signed integer



*Default:*

```nix
null
```



*Example:*

```nix
12345
```

*Declared by:*
 - [modules/programs/github\.nix](../modules/programs/github.nix)


## piste\.github\.key



The private key for this account (its ` .pub ` is the signing key)\. ` piste github ` makes it\.



*Type:*
string



*Default:*

```nix
"${config.home.homeDirectory}/.ssh/id_ed25519_github_<login>"
```

*Declared by:*
 - [modules/programs/github\.nix](../modules/programs/github.nix)


## piste\.github\.login



The GitHub account’s login\. When set, github\.com uses its key over SSH, commits are signed with it, and (with ` id `) the git email is the account’s noreply address\.



*Type:*
null or string



*Default:*

```nix
null
```



*Example:*

```nix
"jane"
```

*Declared by:*
 - [modules/programs/github\.nix](../modules/programs/github.nix)


## piste\.homebrew\.extraTaps



Additional homebrew taps to merge with the base set



*Type:*
attribute set of absolute path



*Default:*

```nix
{ }
```

*Declared by:*
 - [modules/darwin/homebrew-base\.nix](../modules/darwin/homebrew-base.nix)


## piste\.homebrew\.trustedCasks



Casks from non-official taps to trust (see trustedFormulae)\.



*Type:*
list of string



*Default:*

```nix
[ ]
```

*Declared by:*
 - [modules/darwin/homebrew-base\.nix](../modules/darwin/homebrew-base.nix)


## piste\.homebrew\.trustedFormulae



Formulae from non-official taps to trust (Homebrew 6 tap-trust)\.
Rendered declaratively into ~/\.homebrew/trust\.json instead of relying on
interactive ` brew trust `\. Trust dependencies explicitly too — a trusted
formula’s tapped deps are NOT covered transitively (e\.g\. krunkit’s
libkrun-efi must be listed on its own)\.



*Type:*
list of string



*Default:*

```nix
[ ]
```

*Declared by:*
 - [modules/darwin/homebrew-base\.nix](../modules/darwin/homebrew-base.nix)


## piste\.homebrew\.trustedTaps



Whole taps to trust\. Prefer trustedFormulae/trustedCasks for standalone
entries; use this for cohesive single-author taps whose formulae depend
on each other (e\.g\. slp/krunkit → libkrun-efi → virglrenderer), where
enumerating every dependency is brittle\.



*Type:*
list of string



*Default:*

```nix
[ ]
```

*Declared by:*
 - [modules/darwin/homebrew-base\.nix](../modules/darwin/homebrew-base.nix)


## piste\.packages\.brews\.enable



Whether to enable Homebrew brews (CLI via brew)\.



*Type:*
boolean



*Default:*

```nix
true
```



*Example:*

```nix
true
```

*Declared by:*
 - [modules/packages-module\.nix](../modules/packages-module.nix)


## piste\.packages\.brews\.exclude



Brew names to leave out, from the engine’s list and ` extra ` alike



*Type:*
list of string



*Default:*

```nix
[ ]
```

*Declared by:*
 - [modules/packages-module\.nix](../modules/packages-module.nix)


## piste\.packages\.brews\.extra



Additional brews to install alongside the shared set



*Type:*
list of string



*Default:*

```nix
[ ]
```

*Declared by:*
 - [modules/packages-module\.nix](../modules/packages-module.nix)


## piste\.packages\.casks\.enable



Whether to enable Homebrew casks (GUI apps)\.



*Type:*
boolean



*Default:*

```nix
true
```



*Example:*

```nix
true
```

*Declared by:*
 - [modules/packages-module\.nix](../modules/packages-module.nix)


## piste\.packages\.casks\.exclude



Cask names to leave out, from the engine’s list, the extras role’s and ` extra ` alike (e\.g\. MDM-managed apps like slack, zoom)



*Type:*
list of string



*Default:*

```nix
[ ]
```



*Example:*

```nix
[
  "slack"
  "zoom"
  "google-chrome"
]
```

*Declared by:*
 - [modules/packages-module\.nix](../modules/packages-module.nix)


## piste\.packages\.casks\.extra



Additional casks to install alongside the shared set



*Type:*
list of string



*Default:*

```nix
[ ]
```

*Declared by:*
 - [modules/packages-module\.nix](../modules/packages-module.nix)


## piste\.packages\.cli\.enable



Whether to enable CLI tools (bat, ripgrep, fd, etc\.)\.



*Type:*
boolean



*Default:*

```nix
true
```



*Example:*

```nix
true
```

*Declared by:*
 - [modules/packages-module\.nix](../modules/packages-module.nix)


## piste\.packages\.darwinOnly\.enable



Whether to enable Darwin-only packages (terminal-notifier)\.



*Type:*
boolean



*Default:*

```nix
true
```



*Example:*

```nix
true
```

*Declared by:*
 - [modules/packages-module\.nix](../modules/packages-module.nix)


## piste\.packages\.dev\.enable



Whether to enable Development tools (gh, docker, python, node, etc\.)\.



*Type:*
boolean



*Default:*

```nix
true
```



*Example:*

```nix
true
```

*Declared by:*
 - [modules/packages-module\.nix](../modules/packages-module.nix)


## piste\.packages\.exclude



Package names (lib\.getName) to drop from the enabled categories\.
Does not apply to extraPackages\.



*Type:*
list of string



*Default:*

```nix
[ ]
```



*Example:*

```nix
[
  "docker"
]
```

*Declared by:*
 - [modules/packages-module\.nix](../modules/packages-module.nix)


## piste\.packages\.extraPackages



Additional Nix packages to install alongside enabled categories



*Type:*
list of package



*Default:*

```nix
[ ]
```

*Declared by:*
 - [modules/packages-module\.nix](../modules/packages-module.nix)


## piste\.packages\.fonts\.enable



Whether to enable Nerd Fonts and standard fonts\.



*Type:*
boolean



*Default:*

```nix
true
```



*Example:*

```nix
true
```

*Declared by:*
 - [modules/packages-module\.nix](../modules/packages-module.nix)


## piste\.packages\.media\.enable



Whether to enable Media tools (ffmpeg)\.



*Type:*
boolean



*Default:*

```nix
true
```



*Example:*

```nix
true
```

*Declared by:*
 - [modules/packages-module\.nix](../modules/packages-module.nix)


## piste\.packages\.networking\.enable



Whether to enable Networking tools\.



*Type:*
boolean



*Default:*

```nix
true
```



*Example:*

```nix
true
```

*Declared by:*
 - [modules/packages-module\.nix](../modules/packages-module.nix)


## piste\.packages\.pnpm



pnpm global packages the engine installs on ` piste switch `, when they
aren’t installed yet\. ` piste pkgs update ` upgrades the unpinned ones to
their latest version; an entry written ` name@1.2.3 ` is pinned and is
left alone\.



*Type:*
list of string



*Default:*

```nix
[ ]
```



*Example:*

```nix
[
  "tsx"
  "serve@14.2.0"
]
```

*Declared by:*
 - [modules/programs/pnpm\.nix](../modules/programs/pnpm.nix)


## piste\.packages\.security\.enable



Whether to enable Security tools (age, gnupg, yubikey)\.



*Type:*
boolean



*Default:*

```nix
true
```



*Example:*

```nix
true
```

*Declared by:*
 - [modules/packages-module\.nix](../modules/packages-module.nix)


## piste\.packages\.spelling\.enable



Whether to enable Spelling and dictionaries\.



*Type:*
boolean



*Default:*

```nix
true
```



*Example:*

```nix
true
```

*Declared by:*
 - [modules/packages-module\.nix](../modules/packages-module.nix)


## piste\.packages\.terminal\.enable



Whether to enable Terminal emulators and shells\.



*Type:*
boolean



*Default:*

```nix
true
```



*Example:*

```nix
true
```

*Declared by:*
 - [modules/packages-module\.nix](../modules/packages-module.nix)


## piste\.programs\.herdr\.enable



Whether to enable herdr’s settings: the Herdr app’s telemetry off unless set\.



*Type:*
boolean



*Default:*

```nix
false
```



*Example:*

```nix
true
```

*Declared by:*
 - [modules/programs/herdr\.nix](../modules/programs/herdr.nix)


## piste\.programs\.herdr\.app\.settings



Keys merged into the Herdr app’s settings\.json on every switch (Mac
only)\. Declared keys win; the rest of the file is left as it is\.



*Type:*
JSON value



*Default:*

```nix
{ }
```



*Example:*

```nix
{
  telemetry = true;
}
```

*Declared by:*
 - [modules/programs/herdr\.nix](../modules/programs/herdr.nix)


## piste\.programs\.vscode\.enable



Whether to enable VS Code with shared settings, keybindings, and core extensions\.



*Type:*
boolean



*Default:*

```nix
false
```



*Example:*

```nix
true
```

*Declared by:*
 - [modules/programs/vscode\.nix](../modules/programs/vscode.nix)


## piste\.programs\.vscode\.extraExtensions



Extensions to add on top of the shared core set (host-specific opt-ins)\.



*Type:*
list of package



*Default:*

```nix
[ ]
```



*Example:*

```nix
with pkgs.vscode-marketplace; [ vue.volar nuxt.mdc ]

```

*Declared by:*
 - [modules/programs/vscode\.nix](../modules/programs/vscode.nix)


## piste\.programs\.zellij\.enable



Whether to enable zellij config (keybindings, zjstatus/zellaude layout, opt-in autostart)\.



*Type:*
boolean



*Default:*

```nix
false
```



*Example:*

```nix
true
```

*Declared by:*
 - [modules/programs/zellij\.nix](../modules/programs/zellij.nix)


## piste\.programs\.zellij\.timezone



The zone of the status bar’s clock\. zjstatus can’t read the machine’s zone, so set yours\.



*Type:*
string



*Default:*

```nix
"Etc/UTC"
```



*Example:*

```nix
"America/New_York"
```

*Declared by:*
 - [modules/programs/zellij\.nix](../modules/programs/zellij.nix)


## piste\.roles\.base\.git\.enable



Whether to enable the engine’s git (programs\.git)\.



*Type:*
boolean



*Default:*

```nix
true
```



*Example:*

```nix
true
```

*Declared by:*
 - [modules/roles/base\.nix](../modules/roles/base.nix)


## piste\.roles\.base\.shell\.enable



Whether to enable the engine’s zsh (programs\.zsh)\.



*Type:*
boolean



*Default:*

```nix
true
```



*Example:*

```nix
true
```

*Declared by:*
 - [modules/roles/base\.nix](../modules/roles/base.nix)


## piste\.roles\.base\.ssh\.enable



Whether to enable the engine’s SSH client (programs\.ssh)\.



*Type:*
boolean



*Default:*

```nix
true
```



*Example:*

```nix
true
```

*Declared by:*
 - [modules/roles/base\.nix](../modules/roles/base.nix)


## piste\.roles\.base\.starship\.enable



Whether to enable the starship prompt (programs\.starship)\.



*Type:*
boolean



*Default:*

```nix
true
```



*Example:*

```nix
true
```

*Declared by:*
 - [modules/roles/base\.nix](../modules/roles/base.nix)


## piste\.roles\.container\.mutableFiles



Paths under $HOME that the image copies as real, user-owned files
instead of symlinks into the read-only store\. Use it for config files
a tool rewrites at runtime\.



*Type:*
list of string



*Default:*

```nix
[ ]
```



*Example:*

```nix
[
  ".config/gh/config.yml"
]
```

*Declared by:*
 - [modules/roles/container\.nix](../modules/roles/container.nix)


## piste\.roles\.desktop\.alacritty\.enable



Whether to enable the engine’s Alacritty (programs\.alacritty)\.



*Type:*
boolean



*Default:*

```nix
true
```



*Example:*

```nix
true
```

*Declared by:*
 - [modules/roles/desktop\.nix](../modules/roles/desktop.nix)


## piste\.roles\.desktop\.ghostty\.enable



Whether to enable the engine’s Ghostty settings (ghostty/config)\.



*Type:*
boolean



*Default:*

```nix
true
```



*Example:*

```nix
true
```

*Declared by:*
 - [modules/roles/desktop\.nix](../modules/roles/desktop.nix)


## piste\.roles\.dev\.editor\.enable



Whether to enable the engine’s vim (programs\.vim)\.



*Type:*
boolean



*Default:*

```nix
true
```



*Example:*

```nix
true
```

*Declared by:*
 - [modules/roles/dev\.nix](../modules/roles/dev.nix)


## piste\.roles\.dev\.tmux\.enable



Whether to enable the engine’s tmux (programs\.tmux)\.



*Type:*
boolean



*Default:*

```nix
true
```



*Example:*

```nix
true
```

*Declared by:*
 - [modules/roles/dev\.nix](../modules/roles/dev.nix)


## piste\.roles\.extras\.casks\.enable



Whether to enable the extras pack’s extra Homebrew casks and the default apps for file types\.



*Type:*
boolean



*Default:*

```nix
true
```



*Example:*

```nix
true
```

*Declared by:*
 - [modules/extras/casks\.nix](../modules/extras/casks.nix)


## piste\.roles\.extras\.editor\.enable



Whether to enable the extras pack’s editor settings\.



*Type:*
boolean



*Default:*

```nix
true
```



*Example:*

```nix
true
```

*Declared by:*
 - [modules/extras/editor\.nix](../modules/extras/editor.nix)


## piste\.roles\.extras\.ghostty\.enable



Whether to enable the extras pack’s Ghostty settings\.



*Type:*
boolean



*Default:*

```nix
true
```



*Example:*

```nix
true
```

*Declared by:*
 - [modules/extras/ghostty\.nix](../modules/extras/ghostty.nix)


## piste\.roles\.extras\.macos\.enable



Whether to enable the extras pack’s macOS input and system settings\.



*Type:*
boolean



*Default:*

```nix
true
```



*Example:*

```nix
true
```

*Declared by:*
 - [modules/extras/macos\.nix](../modules/extras/macos.nix)


## piste\.roles\.extras\.shell\.enable



Whether to enable the extras pack’s shell aliases and settings\.



*Type:*
boolean



*Default:*

```nix
true
```



*Example:*

```nix
true
```

*Declared by:*
 - [modules/extras/shell\.nix](../modules/extras/shell.nix)


## piste\.roles\.extras\.tmux\.enable



Whether to enable the extras pack’s tmux plugins and settings\.



*Type:*
boolean



*Default:*

```nix
true
```



*Example:*

```nix
true
```

*Declared by:*
 - [modules/extras/tmux\.nix](../modules/extras/tmux.nix)


## piste\.roles\.extras\.tmux\.resurrectExtraConfig



tmux lines placed with resurrect’s own settings, before it loads\. The
pack leaves ` @resurrect-capture-pane-contents ` off (its default):
turned on, a save writes each pane’s visible text to
` ~/.cache/tmux/resurrect ` unencrypted, so a token on screen is on
disk\. The example turns it on\.



*Type:*
strings concatenated with “\\n”



*Default:*

```nix
""
```



*Example:*

```nix
''
  set -g @resurrect-capture-pane-contents 'on'
  set -g @resurrect-pane-contents-area 'visible'
''

```

*Declared by:*
 - [modules/extras/tmux\.nix](../modules/extras/tmux.nix)


## piste\.secrets\.enable



Whether to enable shell environment secret injection via sops-nix\.



*Type:*
boolean



*Default:*

```nix
false
```



*Example:*

```nix
true
```

*Declared by:*
 - [modules/programs/secrets\.nix](../modules/programs/secrets.nix)


## piste\.secrets\.ageKeyFile



Path to age key file for SOPS decryption\.
Defaults to ~/\.config/sops/age/keys\.txt\.



*Type:*
string



*Default:*

```nix
"/home/jane/.config/sops/age/keys.txt"
```

*Declared by:*
 - [modules/programs/secrets\.nix](../modules/programs/secrets.nix)


## piste\.secrets\.envFileName



Name of the rendered ` KEY=value ` env file (a sops template) holding
every ` envVars ` entry\. Consume it from a systemd user unit with
` EnvironmentFile = config.piste.secrets.envFilePath; ` so that
non-interactive, service-launched processes get the same secrets the
interactive zsh exports give you\.



*Type:*
string



*Default:*

```nix
"piste-secrets.env"
```

*Declared by:*
 - [modules/programs/secrets\.nix](../modules/programs/secrets.nix)


## piste\.secrets\.envFilePath



Path to the rendered env file\. Empty when ` envVars ` is empty\.



*Type:*
string *(read only)*



*Default:*

```nix
config.sops.templates.${cfg.envFileName}.path
```

*Declared by:*
 - [modules/programs/secrets\.nix](../modules/programs/secrets.nix)


## piste\.secrets\.envVars



Map of environment variable names to SOPS key paths\.
Each entry creates a sops\.secrets declaration and a shell export\.



*Type:*
attribute set of string



*Default:*

```nix
{ }
```



*Example:*

```nix
{
  ANTHROPIC_API_KEY = "anthropic/api_key";
  OPENROUTER_API_KEY = "openrouter/api_key";
}

```

*Declared by:*
 - [modules/programs/secrets\.nix](../modules/programs/secrets.nix)


## piste\.secrets\.sopsFile



Path to SOPS-encrypted YAML file containing secrets (set by overlay repos)\.



*Type:*
null or absolute path



*Default:*

```nix
null
```

*Declared by:*
 - [modules/programs/secrets\.nix](../modules/programs/secrets.nix)
