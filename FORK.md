# Bounded shared-dispatch experiment

Base: c3400c312a739cf7598204a981156959910ed874. License unchanged: PolyForm Shield.
This local branch is an experiment, not a deployable PS4 server. The regular
entry point still selects Nintendo; tests inject psnPlatform explicitly into
newPlatformAuthHandler. No global authentication mode flag is added.

The experiment keeps the original dispatcher and matchmaking engine, accepts CTR
titles 5760/5775 in shared CTR handlers, adds PS4 metadata and bandwidth handlers,
and reuses the existing 194/1 handler. NEXTENDO_REDACT_LOGS defaults to redaction;
setting it to 0 opts into inherited sensitive diagnostics and must not be used
with real accounts. Raw dumps are disabled under redaction for every platform.
Authenticated lobby reads have no idle deadline; request/handshake timeouts remain.

The normal full suite, vet and build are separate from deployment gates.
Run go test -tags=acceptance -run '^TestAcceptance' ./... to expose the remaining
unissued-ticket, response-signature and 12/10 lookup failures. Those failures are
intentional acceptance requirements, not waived checks. Do not deploy this branch.
The current fork's adapter regression suite is retained on its review branch;
its types cannot simply be transplanted to this different implementation.

Detailed evidence and recommendation: workspace docs/NEXTENDO_UPSTREAM_EXPERIMENT.md.
