#!/usr/bin/env bash
# Build a Linux release of Bifrost Terminal (make/bifrosterm-linux-x64-<version>.pacman).
# Install it afterwards with: sudo ./install.sh
#
# Like scripts/package-local.ps1, this avoids `task package`: its `clean` step deletes dist/ in parallel with
# build:backend, and Task's checksum cache can then leave an installer with no wavesrv. Backend steps run with --force.
set -euo pipefail
cd "$(dirname "$(readlink -f "$0")")"

if command -v go-task >/dev/null 2>&1; then
    TASK=go-task
elif command -v task >/dev/null 2>&1; then
    TASK=task
else
    echo "Task not found. Install it: sudo pacman -S go-task" >&2
    exit 1
fi

# node 26+ breaks `npm install`, so prefer an nvm-installed node 24 for this run only
node_major() { node --version 2>/dev/null | sed -E 's/^v([0-9]+)\..*/\1/'; }
if [ "$(node_major)" != "24" ]; then
    nvm_root="${nvm_data:-${XDG_DATA_HOME:-$HOME/.local/share}/nvm}"
    node24="$(ls -d "$nvm_root"/v24.* 2>/dev/null | sort -V | tail -n 1 || true)"
    if [ -n "$node24" ] && [ -x "$node24/bin/node" ]; then
        export PATH="$node24/bin:$PATH"
    else
        echo "Node 24 not found (current: $(node --version 2>/dev/null || echo none)). Install it with nvm." >&2
        exit 1
    fi
fi
echo "Using node $(node --version)"

step() { printf '\n==> %s\n' "$1"; }

step "Version bump"
# patch number only; bump major/minor by hand in package.json. --no-git-tag-version leaves package.json and package-lock.json modified for you to commit
npm version patch --no-git-tag-version >/dev/null
echo "Version $(node -p "require('./package.json').version")"

rm -rf make

step "Backend (wavesrv, wsh, tsunami scaffold)"
"$TASK" build:backend build:tsunamiscaffold --force

step "Frontend (production build)"
npm run build:prod

step "Package (electron-builder, pacman only)"
maintainer_email="$(git config user.email || true)"
# package, executable, desktop file and icons are named bifrosterm so the install replaces the earlier bifrosterm
# package and never collides with waveterm-bin, which owns /usr/bin/waveterm, waveterm.desktop and the waveterm icons
npx electron-builder -c electron-builder.config.cjs -p never --linux pacman \
    -c.extraMetadata.name=bifrosterm \
    -c.linux.executableName=bifrosterm \
    -c.extraMetadata.author.email="${maintainer_email:-heinsutton@users.noreply.github.com}"

pkg="$(ls make/bifrosterm-linux-x64-*.pacman 2>/dev/null | head -n 1 || true)"
if [ -z "$pkg" ]; then
    echo "Package not found in make/" >&2
    exit 1
fi
if [ ! -f make/linux-unpacked/resources/app.asar.unpacked/dist/bin/wavesrv.x64 ]; then
    echo "Packaged app is missing wavesrv; the package would not start its backend" >&2
    exit 1
fi

printf '\nBuilt %s\nInstall it with: sudo ./install.sh\n' "$pkg"
