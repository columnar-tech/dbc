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

export interface InitResponse {
  driver_list_path: string;
  created: boolean;
}

export interface AddResponseDriver {
  name: string;
  version_constraint?: string;
}

export interface AddResponse {
  driver_list_path: string;
  driver: AddResponseDriver;
}

export interface RemoveResponseDriver {
  name: string;
}

export interface RemoveResponse {
  driver_list_path: string;
  driver: RemoveResponseDriver;
}
