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

# dbc docs

dbc uses [Material for MkDocs](https://squidfunk.github.io/mkdocs-material/) for docs.
Building the docs requires a [Pixi](https://pixi.sh/) installation.

## Building

To build the docs website, run,

```sh
pixi run docs-build
```

The built docs website will be in `./site` and you can preview it by running this (or something similar):

```sh
python -m http.server -d site
```

## Developing

mkdocs has live preview+reload. To use it while developing, run:

```sh
pixi run docs-serve
```
