## Summary

<!-- What does this PR change, and why? -->

## Linked issue

<!-- Use `Closes #...` where this PR fully resolves an issue, otherwise `Refs #...`. -->

## How it was tested

<!-- Commands run, tests added or updated, manual checks. -->

## Checklist

- [ ] The PR title follows [Conventional Commits](https://www.conventionalcommits.org/), using a type from `release-please-config.json` (`feat`, `fix`, `perf`, `revert`, `docs`, `chore`, `refactor`, `test`, `build`, `ci`, `style`); breaking changes are marked with `!`.
  <!-- release-please builds the changelog from the commit messages that land on `main`; with a squash merge that is the PR title. Only `feat`, `fix`, `perf` and `revert` appear in the changelog; the other types are hidden. -->
- [ ] `go build ./... && go vet ./... && go test ./...` passes locally (there is no CI test job).
- [ ] `gofmt` was run on changed files only. Some files in `internal/` are already gofmt-dirty, so `gofmt -w` on a whole directory rewrites unrelated files.
- [ ] README / `docs/` / `CHANGELOG`-relevant behaviour is documented.
