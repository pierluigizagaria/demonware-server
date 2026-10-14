# Independent repository transition

Decision: 2026-10-14. After completing the architecture refactor at `120c965`,
the user requested publishing the complete current project in a new independent
GitHub repository, deleting the old GitHub fork, rewriting the README and
consolidating the credits. The confirmed name is **demonware-server**.

- New canonical repository: `pierluigizagaria/demonware-server`, default branch `main`.
- Previous GitHub fork: `pierluigizagaria/nextendo-demonware`.
- The full ancestry of both previously published branches is retained in `main`.
  Local experimental/snapshot refs are preserved in a private verified Git bundle;
  they are not additional published product branches.
- `refactor/ctrnf-architecture` remains the `120c965` review checkpoint;
  `feat/ps4-platform` remains the accepted `33dc1c6` checkpoint.
- The module path and internal imports become
  `github.com/pierluigizagaria/demonware-server`. Dependencies and `go.sum` do not change.
- The source command remains `cmd/ctrnf-server`; the documented executable name
  is `demonware-server`. The existing local component directory remains
  `nextendo-demonware/` to preserve workspace/lab paths.
- README is rewritten for the independent project. `CREDITS.md` preserves the
  original authors, upstream contributions and research references.
- `LICENSE.md`, including `Required Notice: Copyright 2026 Nextendo Network`,
  is unchanged. This is still derived work under PolyForm Shield.

This decision supersedes the architecture task's no-push restriction for this
repository transition only. Historical architecture reports remain records of
the pre-publication task: their unchanged-module/no-push statements describe
that completed task, not this later naming/publication change. No runtime
protocol, admission, schema, SQL, economy policy, service or lab installation
changes are part of the transition.

The old fork is deleted only after the new repository's branch and tree are
verified against the local committed source. Its pre-migration inventory had
two branches, no pull requests or releases, no forks or stars, and disabled
issues/wiki. Both remote branch heads were already ancestors of `120c965`.
