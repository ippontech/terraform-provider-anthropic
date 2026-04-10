---
name: ippon-cd-install-tools
description: Installs tools on your environment such as java, npm, node, terraform, go,. Use when installing a new tool, updating an existing one or uninstalling one.
model: sonnet
---

# Quickstart

Installing mise:
```shell
curl https://mise.run | sh
```

By default, mise installs to `~/.local/bin`, but it can go anywhere.

Verify the installation:
```shell
~/.local/bin/mise --version
# mise 2024.x.x
```

`~/.local/bin` does not need to be in PATH. mise will automatically add its own directory to PATH when activated.

Hook mise into your shell (pick the right one for your shell):

```sh-session
# note this assumes mise is located at ~/.local/bin/mise
# which is what https://mise.run does by default
echo 'eval "$(~/.local/bin/mise activate bash)"' >> ~/.bashrc
echo 'eval "$(~/.local/bin/mise activate zsh)"' >> ~/.zshrc
echo '~/.local/bin/mise activate fish | source' >> ~/.config/fish/config.fish
echo '~/.local/bin/mise activate pwsh | Out-String | Invoke-Expression' >> ~/.config/powershell/Microsoft.PowerShell_profile.ps1
```

## Execute commands with specific tools

```sh-session
$ mise exec node@24 -- node -v
mise node@24.x.x ✓ installed
v24.x.x
```

## Version pinning

Always pin tools to exact versions in `mise.toml` to prevent unexpected upgrades. Never use major-only (`"1"`) or minor-only (`"1.26"`) constraints:

```toml
# Correct — exact version, no surprises
[tools]
go = "1.26.2"
terraform = "1.14.8"

# Wrong — allows patch or minor upgrades
[tools]
go = "1.26"
terraform = "1"
```

To find the currently installed version before pinning:
```shell
mise ls --current
```

## Install tools

```sh-session
$ mise use --global node@24 go@1
$ node -v
v24.x.x
$ go version
go version go1.x.x macos/arm64
```

See [dev tools](https://mise.jdx.dev/dev-tools/) for more examples.

## Manage environment variables

```toml
# mise.toml
[env]
SOME_VAR = "foo"
```

```sh-session
$ mise set SOME_VAR=bar
$ echo $SOME_VAR
bar
```

Note that `mise` can also [load `.env` files](https://mise.jdx.dev/environments/#env-directives).

## Run tasks

```toml
# mise.toml
[tasks.build]
description = "build the project"
run = "echo building..."
```

```sh-session
$ mise run build
building...
```

See [tasks](https://mise.jdx.dev/tasks/) for more information.
