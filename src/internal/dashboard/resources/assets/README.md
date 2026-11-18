# Vendored Tabler files

`css/tabler.min.css` and `js/tabler.min.js` are Tabler 1.7.5, copied unchanged from the
npm package `@tabler/core@1.7.5` (`dist/css/tabler.min.css` and `dist/js/tabler.min.js`,
which bundles Bootstrap's JavaScript for the dropdowns and the collapsing menu). MIT
licence; the licence banner is each file's first comment.

- Tarball: https://registry.npmjs.org/@tabler/core/-/core-1.7.5.tgz
- Tarball sha256: `fce73d0d136e5866cd90f69e4df94b823bd2eb187776a2755b8f7d993f825f42`
- css/tabler.min.css sha256: `d1708c69e6b07a5a3138b8549bf64e00cd9d18c130b9b2ba2d9e294956f7c564`
- js/tabler.min.js sha256: `f63744ad8e12180089844d0e550c5b3abd66eda0e52dead2eb31eef017148e93`
- Docs: https://docs.tabler.io

The icons in `internal/dashboard/icons.go` are drawn in the style of Tabler Icons
(MIT, https://tabler.io/icons): 24px, 2px stroke, round joins.

To update, replace both files from the new version's tarball, change the version and
hashes here, and check every page still renders (`make dashboard`).
