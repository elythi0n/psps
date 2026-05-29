# Maintainer: Elythi0n <info@marcosraudkett.com>
#
# Build & install:
#   makepkg -si
# Or via the Makefile:
#   make pkg          # syncs pkgver from VERSION, then makepkg -si
#
# Builds from the local working tree — no remote source is fetched. To install
# on a fresh machine, `git clone` the repo first, then run makepkg there.

pkgname=psps
pkgver=0.2.0
pkgrel=1
pkgdesc="TUI for managing kitty (themes, sessions, keybinds, fonts)"
arch=('x86_64')
url="https://github.com/elythi0n/psps"
license=('MIT')
depends=('kitty' 'fontconfig')
makedepends=('go>=1.22')
optdepends=(
  'ttf-jetbrains-mono-nerd: a popular Nerd Font that pairs well with the bundled themes'
)
provides=("$pkgname")
conflicts=("$pkgname-git")
source=()
sha256sums=()

prepare() {
  # Stage the source tree under $srcdir so the build doesn't pollute $startdir.
  # Only the files Go actually needs are copied — keeps the staging dir small
  # and avoids carrying .git, build/, etc. through pacman packaging.
  install -d "$srcdir/$pkgname-$pkgver"
  cp -r \
    "$startdir/main.go" \
    "$startdir/go.mod" \
    "$startdir/go.sum" \
    "$startdir/internal" \
    "$srcdir/$pkgname-$pkgver/"
}

build() {
  cd "$srcdir/$pkgname-$pkgver"
  export CGO_ENABLED=0
  export GOFLAGS="-trimpath -mod=readonly -modcacherw"
  go build \
    -ldflags "-s -w -X github.com/elythi0n/psps/internal/cli.version=v$pkgver" \
    -o "build/$pkgname" \
    .
}

package() {
  cd "$srcdir/$pkgname-$pkgver"
  install -Dm755 "build/$pkgname" "$pkgdir/usr/bin/$pkgname"
}
