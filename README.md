# diff

[![License](https://img.shields.io/github/license/FollowTheProcess/diff)](https://github.com/FollowTheProcess/diff)
[![Go Reference](https://pkg.go.dev/badge/go.followtheprocess.codes/diff.svg)](https://pkg.go.dev/go.followtheprocess.codes/diff)
[![Go Report Card](https://goreportcard.com/badge/github.com/FollowTheProcess/diff)](https://goreportcard.com/report/github.com/FollowTheProcess/diff)
[![GitHub](https://img.shields.io/github/v/release/FollowTheProcess/diff?logo=github&sort=semver)](https://github.com/FollowTheProcess/diff)
[![CI](https://github.com/FollowTheProcess/diff/workflows/CI/badge.svg)](https://github.com/FollowTheProcess/diff/actions?query=workflow%3ACI)
[![codecov](https://codecov.io/gh/FollowTheProcess/diff/branch/main/graph/badge.svg)](https://codecov.io/gh/FollowTheProcess/diff)

![demo](https://github.com/FollowTheProcess/diff/raw/main/docs/img/demo.gif)

A pure Go text diff library providing an anchored diff algorithm, character-level
inline diff, and colourised terminal rendering. It started life as `internal/diff` in [`test`] until
I needed it in a few places so here it is 🎉

## Project Description

`diff` computes the difference between two texts using an *anchored diff* algorithm.
Unlike standard diff, which finds the smallest edit by line count, the anchored
algorithm matches on lines that appear exactly once in both old and new — unique
lines act as anchors, preventing unrelated blank lines or closing braces from being
reused as false matches. The result is typically cleaner and more readable output,
and the algorithm runs in O(n log n) rather than O(n²).

The library is split into two packages:

- **`go.followtheprocess.codes/diff`** — the diff algorithm and all types. No
  external dependencies.
- **`go.followtheprocess.codes/diff/render`** — colourised terminal rendering
  using ANSI escape codes via [`hue`]. Import only when you need colour output.

## Installation

```shell
go get go.followtheprocess.codes/diff@latest
```

For colourised rendering also get the render subpackage (its `hue` dependency is
pulled in automatically):

```shell
go get go.followtheprocess.codes/diff/render@latest
```

## Quickstart

### Plain unified diff

```go
package main

import (
    "os"

    "go.followtheprocess.codes/diff"
)

func main() {
    old := []byte("hello\nworld\n")
    new := []byte("hello\nearth\n")

    // Returns nil when inputs are equal.
    out := diff.Diff("old.txt", old, "new.txt", new)
    os.Stdout.Write(out)
}
```

Output:

```
diff old.txt new.txt
--- old.txt
+++ new.txt
@@ -1,2 +1,2 @@
  hello
- world
+ earth
```

### Colourised terminal rendering

```go
package main

import (
    "os"

    "go.followtheprocess.codes/diff"
    "go.followtheprocess.codes/diff/render"
)

func main() {
    old := []byte("hello\nworld\n")
    new := []byte("hello\nearth\n")

    lines := diff.Lines("old.txt", old, "new.txt", new)
    os.Stdout.Write(render.Render(lines))
}
```

`render.Render` applies ANSI colour: red for removed lines, green for added lines,
bold for headers. When a removed and added line appear as a 1:1 pair, changed
*characters* are highlighted with a coloured background for precise inline diffing.

### Character-level diff

```go
ic := diff.CharDiff([]byte("hello world\n"), []byte("hello earth\n"))

for _, seg := range ic.Removed {
    fmt.Printf("removed segment changed=%v: %q\n", seg.Changed, seg.Text)
}
for _, seg := range ic.Added {
    fmt.Printf("added  segment changed=%v: %q\n", seg.Changed, seg.Text)
}
```

## API Overview

| Symbol | Package | Description |
|--------|---------|-------------|
| `Lines` | `diff` | Structured `[]Line` output; nil if inputs are equal |
| `Diff` | `diff` | Raw unified-diff `[]byte`; nil if inputs are equal |
| `CharDiff` | `diff` | Character-level segment diff for a removed/added line pair |
| `Render` | `diff/render` | Colourised `[]byte` for terminal output |

## Acknowledgements

The core diff algorithm (`diff.go`) is derived from the Go standard library's
[`internal/diff`](https://github.com/golang/go/tree/master/src/internal/diff) package.

Copyright 2022 The Go Authors. All rights reserved. Used under a
[BSD-style license](https://github.com/golang/go/blob/master/LICENSE).

Several optimisations — including common prefix/suffix trimming, the disjoint
fast-path, and the `CharDiff` similarity ratio gate — were inspired by the
[`similar`](https://github.com/mitsuhiko/similar) Rust crate by Armin Ronacher.

### Credits

This package was created with [copier] and the [FollowTheProcess/go-template] project template.

[copier]: https://copier.readthedocs.io/en/stable/
[FollowTheProcess/go-template]: https://github.com/FollowTheProcess/go-template
[`hue`]: https://go.followtheprocess.codes/hue
[`test`]: https://go.followtheprocess.codes/test
