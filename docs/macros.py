# Copyright 2026 Columnar Technologies Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.


def define_env(env):
    @env.macro
    def since_version(version):
        """Create a "since v1.2.3" badge for annotation featuresw ith.

        Args:
            version: git tag for version
        """
        return (
            f'<span class="version-badge">'
            f'<a href="https://github.com/columnar-tech/dbc/releases/{version}" target="_blank">SINCE {version}</a>'
            f'</span>'
        )
