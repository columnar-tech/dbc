// Copyright 2026 Columnar Technologies Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

export interface SearchDriverBasic {
  driver: string;
  description: string;
  installed?: string[];
  registry?: string;
}

export interface SearchDriverVerbose {
  driver: string;
  description: string;
  license: string;
  registry?: string;
  installed_versions?: Record<string, string[]>;
  available_versions?: string[];
}

export interface SearchResponse {
  drivers: SearchDriverBasic[] | SearchDriverVerbose[];
  warning?: string;
}
