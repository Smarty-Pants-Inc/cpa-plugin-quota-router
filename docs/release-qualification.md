# Exact-pair release qualification

A plugin version, mapped filename, successful compilation and tested native pair
are different facts. The default process fixture builds the v7.2.100 SDK module;
it does not establish compatibility with every newer host. The historical gateway
`4b5f1eab25fca4b3815369a826e958e7c070a69e` and its tool-prefix diagnostic remain
separate evidence, not a current release destination.

## Freeze the pair before qualification

The gateway/plugin integrator records one immutable candidate with:

- Plugin repository, exact commit/tree, version, unchanged go.mod/go.sum digests.
- Gateway repository, exact commit/tree/version and source/archive digest. The
  maintained-host proposal is in [host-contract.md](host-contract.md); it is not
  yet an accepted or patched pair.
- Exact Go patch version, `CGO_ENABLED`, C compiler/linker versions and native
  OS/architecture. Record Linux libc/minimum runtime requirements, or the Darwin
  deployment target / Windows runtime closure as applicable.
- Exact build command, flags, environment allowlist and native build runner.
- Archive and extracted-library SHA256, size and package member list, plus the
  gateway artifact digest. Both libraries rebuilt for fixtures and actual
  packaged bytes must be distinguished.

No source or artifact field is filled with a guessed future value. Build release
candidates from the exact landed commits. Retain the resulting evidence beside
the staged archives using the existing release/CI artifact path; no new service
or registry is required.

## Receive checks at their actual boundary

CI placement and candidate isolation remain with the existing CI owner. Supply
exact prepared source and dependency/toolchain inputs, private temporary runtime
state and synthetic credentials. Do not hydrate developer/provider/vault secrets.
Do not use a Dev1 build or automatically allocate paid capacity.

Required existing checks: `make test`, `make test-race`, `make vet`, and the
native package build. Also record formatting and the focused request/worker and
lifecycle regression results. The process fixture has `!race`; a separately built
C-shared library is not instrumented by the parent test's race flag. State that
coverage limit instead of calling it whole-process native race coverage.

Then receive native load/init/call/free/quiesce/reconfigure/shutdown results for
the **same packaged library** with the exact selected gateway. Test status,
quota exhaustion, privacy, management authentication negatives and lifecycle
behavior with synthetic inputs. The proposed eligibility pair needs the full
contract cases in `host-contract.md` before it is supported.

The packaging matrix remains Linux amd64/arm64, Darwin amd64/arm64 and Windows
amd64. Record each platform as compiled, native-qualified, failed, skipped or
not run. A Unix `dlopen` pass is not Windows proof. ARM64 source diagnostics do not
qualify the Linux amd64 running gateway. Never turn an untested target into a
support claim merely because the archive exists. Native support expansion or
narrowing remains an explicit integrator decision.

## Publish and promote without changing tested bytes

Normal tag publication must not overwrite existing release assets. The workflow
uses `gh release upload` without `--clobber`: a duplicate asset name fails rather
than replacing its bytes. A partially completed release can receive missing
assets, but an existing checksum/archive is not rewritten. If published bytes are
wrong, use a new version through review; exceptional replacement needs separate
owner authority and preservation of prior bytes and receiving evidence.

Publish the staged, qualified archives and their checksums/receipt. Verify remote
asset digests against the staged inputs. Promotion installs those same bytes; it
must not rebuild. Release publication, installation, activation and actor-visible
acceptance are separate operations under the runtime owner's authority.

Retain the previous qualified host/plugin pair and required compatible config
outside the plugin directory: the host can prune unselected versions there.
Before activation, verify drain/stream handling and rollback compatibility. A
source revert or retained basename alone is not a working rollback. No deployment,
live-provider probe or restart is authorized by this source task or a test pass.
