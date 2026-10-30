<!--
Copyright 2026 Columnar Technologies Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
-->

# dbc Release Process

Since we rely on `goreleaser` and automation for managing our releases,
the actual release process is exceptionally easy:

```shell
$ git tag -a v0.2.0 -m "dbc v0.2.0 Release"
$ git push --tags
```

For pre-releases, we utilize tags such as `alpha` and `beta`. e.g., to
do a pre-release, you use the following command:

```shell
$ git tag -a v0.2.0-beta1 -m "dbc v0.2.0 Beta Release"
$ git push --tags
```

The automation will take over from the tag being pushed.

## Linux development packages

Releases publish `libdbc-dev_*.deb` and `libdbc-devel*.rpm` alongside the CLI
packages, both on the GitHub release and in the versioned distribution bucket.
These are development-only packages, separate from the `dbc` CLI package; there
is no separate runtime package.

Each package includes `libdbc.a`, `libdbc.so` (SONAME `libdbc.so`),
`/usr/include/libdbc.h`, and the `dbc` pkg-config module. The libraries and
`pkgconfig/dbc.pc` are installed under:

| Package | Architecture | Library directory |
| --- | --- | --- |
| `libdbc-dev` (Debian) | `amd64` | `/usr/lib/x86_64-linux-gnu` |
| `libdbc-dev` (Debian) | `arm64` | `/usr/lib/aarch64-linux-gnu` |
| `libdbc-devel` (RPM) | `x86_64`, `aarch64` | `/usr/lib64` |

Install the package for your architecture, for example:

```shell
sudo apt install ./libdbc-dev_*_amd64.deb pkg-config
# Or, on an RPM-based distribution:
sudo dnf install ./libdbc-devel*.x86_64.rpm pkgconf-pkg-config
pkg-config --modversion dbc
```

The `capi` build supplies the static archive and header. The Linux-only
`capi-shared` build uses the same Zig targets (glibc 2.28) and stages its shared
library and format-specific pkg-config files with `scripts/stage_devel.py` under
`dist/devel/linux_<goarch>/`. The `devel-deb` and `devel-rpm` nFPM entries combine
these artifacts without duplicating the generated header. Snapshot CI uploads
both package formats as the `devel-packages` artifact and tests the installed
packages on Linux amd64 and arm64.

Rust consumers can disable default features and enable `pkg-config` to use these
packages instead of the bundled platform crates. Static linking is the default;
setting `DBC_DYNAMIC` selects the shared library, which must remain on the
runtime loader's search path. See
[Linking a system libdbc](rust/columnar-dbc/README.md#linking-a-system-libdbc)
for the feature and environment-variable options.

## Release Pre-Flight Checklist

Before creating and pushing the tags, please consult the following
checklist of steps:

- [ ] Are the docs up to date with any new/changed features?
- [ ] Are the auto-complete scripts up to date with changes to the
      options and subcommands?
- [ ] If any new features or CLI subcommands/flags have been added in this
  release, add `{{ since_version('v1.2.3') }}` tags in the docs where
  appropriate
- [ ] Is the `llmstxt` plugin config in sync with the main `nav` in
  [mkdocs.yml](mkdocs.yml)?
- [ ] *(Before promoting a pre-release)* Has the pre-release been
      manually tested?
- [ ] After pushing the tag, you will need to approve the deployment
      on the Actions tab of the repo.
- [ ] *(One time)* The `production` environment has a `CARGO_REGISTRY_TOKEN`
      secret for a crates.io account that owns the `columnar-dbc*` crates.
      GoReleaser publishes them, with the release's version, in its final
      `after` hook; a failure there happens after the GitHub release is out.
      To recover, rerun `goreleaser build --clean --id capi` (needs Zig 0.17.0
      and `GORELEASER_KEY`) at the tag, then
      `python3 scripts/publish_crates.py --version <version>`.

Once the above checklist is completed, just push the new tag to
kick off the release process.
Then, continue to the [Post-Release Checklist](#post-release-checklist).

## Post-Release Checklist

- [ ] Create a pull request against [microsoft/winget-pkgs](https://github.com/microsoft/winget-pkgs) from [our fork](https://github.com/columnar-tech/winget-pkgs). See [winget](#winget) for details.

### Details

You can find detailed steps for items in the post-release checklist here.

#### winget

Updating our WinGet package is only partially automated. goreleaser automatically creates a branch at https://github.com/columnar-tech/winget-pkgs but then someone needs to manually create a pull request against upstream with the change.

Follow these steps:

1. Wait for the release workflow to finish.
1. Find the branch at https://github.com/columnar-tech/winget-pkgs. It should be called `dbc-$VERSION`.
1. Create a pull request
