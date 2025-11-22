# pplhatearch

`pplhatearch` is a minimal proof-of-concept clone of the [`yay`](https://github.com/Jguer/yay) package helper. Instead of querying the live AUR RPC API, this tool directly clones the [archlinux/aur](https://github.com/archlinux/aur) GitHub mirror and checks out the branch matching a package name.

## Requirements

- Git (used to clone package branches)
- Go 1.21+ to build from source

## Build

```bash
go build -o pplhatearch .
```

## Usage

```bash
./pplhatearch (-S|-R) [flags] <package> [<package>...]
```

Pacman-style commands:

- `-S` – install (clone) one or more packages
- `-R` – remove the local clone of one or more packages

Additional flags:

- `-C string` – directory to install/remove packages from (default `$XDG_CACHE_HOME/pplhatearch` or `$HOME/.cache/pplhatearch`)
- `-repo string` – Git mirror to use (default `https://github.com/archlinux/aur.git`)
- `-color` – toggle the `yay`/`pacman`-style colorized prefixes (enabled by default)
- `-n` – when used with `-R`, forward to `pacman -n` (do not save config files)
- `-s` – when used with `-R`, forward to `pacman -s` (remove unneeded dependencies)
- `-d` – when used with `-R`, forward to `pacman -d` (skip dependency checks)
- `-f` – when used with `-R`, forward to `pacman -f` (force removal even if dependencies break)
- `-Ss` – search the backup AUR mirror for packages (accepts search terms)
- `-Si` – show package information pulled from `.SRCINFO`
- `-Q` – display information about locally installed packages (delegates to `pacman -Qi`)

Set `PPLHATEARCH_CACHE` to override the default cache directory when `-C` is not provided.

Example: clone the `brave-bin` branch from the mirror into the current directory.

```bash
./pplhatearch -S brave-bin
```

`pplhatearch` clones the requested package branch into your cache directory (updating it on later runs) and then executes `makepkg -si` inside that folder to build and install the package, mirroring `yay`'s behavior. Before building, it parses the package’s `.SRCINFO` (via `makepkg --printsrcinfo`), checks `depends` and `makedepends`, and recursively builds any missing AUR dependencies before attempting the parent build. Official repository dependencies are left to `makepkg`/`pacman`. You can override the cache path with `-C`.

When a build directory already exists and `pplhatearch` is running interactively (TTY), it mirrors yay’s prompts:

```
AUR Explicit (1): mypkg
:: (1/1) Installing mypkg...
  1 mypkg                           (Build Files Exist)
==> Packages to cleanBuild?
==> [N]one [A]ll [Ab]ort [I]nstalled [No]tInstalled or (1 2 3, 1-3, ^4)
==>
  1 mypkg                           (Build Files Exist)
==> Diffs to show?
==> [N]one [A]ll [Ab]ort [I]nstalled [No]tInstalled or (1 2 3, 1-3, ^4)
==>
```

Choosing `A` triggers a clean build (the cache folder is removed and recloned), `Ab` aborts, and the default (`Enter`/`N`) reuses the existing files. Selecting `A` when prompted for diffs runs `git diff` inside the package folder before building. Non-interactive sessions skip these prompts.

Removing a package (and its cached source) can be done with:

```bash
./pplhatearch -R brave-bin
```

Removal first calls `pacman -R` with any extra flags (`-n`, `-s`, `-d`, `-f`) you provide, so sudo may be required. Once pacman succeeds, the cached clone under your pplhatearch cache directory is deleted. If you attempt to remove a package that is not cached, only the pacman step is run.

Short flags can be chained just like pacman, so `./pplhatearch -Rns pkgname` is accepted and expands to `-R -n -s`.

### Query Tool

pplhatearch now includes lightweight query tooling backed by the Git mirror:

- `./pplhatearch -Ss <term>` – lists matching branches (package names) using `git ls-remote` (results cached ~15 minutes under `~/.cache/pplhatearch/branches.cache`).
- `./pplhatearch -Si <pkg>` – fetches the package into the cache (without building) and prints `.SRCINFO` metadata (version, description, depends, etc.).
- `./pplhatearch -Q <pkg>` – delegates to `pacman -Qi` for locally installed packages.

Examples:

```
$ ./pplhatearch -Ss anime
aur/anime4k 7b3c5e21

$ ./pplhatearch -Si anime4k
Repository      : aur
Name            : anime4k
Version         : 4.0.1-3
Description     : High-Quality Anime Upscaling
URL             : https://github.com/bloc97/Anime4K
Depends On      : ffmpeg
Make Deps       : git cmake
License         : MIT

$ ./pplhatearch -Q anime4k
Name            : anime4k
Version         : 4.0.1-3
Description     : High-Quality Anime Upscaling
Depends On      : ffmpeg
...
```

`-Ss` uses the cached branch list to avoid hitting GitHub for every search; use `-C`/`PPLHATEARCH_CACHE` to relocate both build trees and the branch cache file.

### Sample Output

```
$ ./pplhatearch -S brave-bin
:: Installing 1 package(s)
:: Retrieving packages from https://github.com/archlinux/aur.git
:: (1/1) Installing brave-bin...
==> Cloning brave-bin from https://github.com/archlinux/aur.git
Cloning into '/home/user/.cache/pplhatearch/brave-bin'...
remote: Enumerating objects: ...
==> Resolving AUR dependency libfoo
:: (1/1) Installing libfoo...
==> Cloning libfoo from https://github.com/archlinux/aur.git
==> Building libfoo with makepkg
==> Installing libfoo
==> Building brave-bin with makepkg
==> Making package: brave-bin ...
==> Finished making: brave-bin
==> Installing brave-bin
:: Operation completed
```

Use `-color=false` to emit plain ASCII output without ANSI escape sequences.

## Shell Completions

Minimal completion scripts (mirroring yay’s helpers) live in `completions/`:

- `completions/pplhatearch.bash` – source it or copy into `/etc/bash_completion.d/`
- `completions/pplhatearch.zsh` – drop into a directory referenced by `fpath` (e.g. `/usr/share/zsh/site-functions/`)
- `completions/pplhatearch.fish` – copy to `~/.config/fish/completions/`

Each script exposes the `-S`/`-R` workflow and the primary flags; bash completions also fall back to filesystem suggestions when typing package names to mimic yay’s UX. Reload your shell or re-source the files after installation.
