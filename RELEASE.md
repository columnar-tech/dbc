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
