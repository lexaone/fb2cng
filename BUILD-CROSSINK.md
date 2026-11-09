# Personal CrossInk build

Base upstream: https://github.com/rupor-github/fb2cng
Base commit: d20143e808844c5ef61062645befeff31e5c97cb
Build label: 0.0.1-crossink.1
License: GNU GPL v3; see LICENSE.

This source archive includes vendored dependencies, generated enums/version,
TeX hyphenation dictionaries, sentence models and bundled PDF fonts. It does
not require the download/preparation tasks for a normal rebuild.

Use Go 1.27.3 (as specified by the upstream go.mod). From this directory:

```sh
go test -mod=vendor ./config ./fb2 ./content ./convert/epub
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -mod=vendor -trimpath -ldflags='-s -w' -o fbc.exe ./cmd/fbc
```

On Windows set the three environment variables using the shell's syntax, or
simply use `go build -mod=vendor -trimpath -ldflags="-s -w" -o fbc.exe ./cmd/fbc`
on a Windows x64 machine with CGO disabled.

If the temporary filesystem is small, set TMPDIR to a sufficiently large
writable directory. The generated files are normally gitignored upstream;
they are intentionally supplied here for rebuilding this exact distribution.

See docs/crossink-options.md for option semantics. Tests added by this change
are config/crossink_test.go, fb2/images_crossink_test.go and
convert/epub/crossink_test.go. The JPEG fixture is synthetic and progressive.
