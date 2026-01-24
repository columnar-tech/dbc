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

FROM alpine:latest AS base

# Notes
# 1. `ca-certificates` is so we can use TLS (i.e., so dbc search works)
# 2. Creating /tmp is so we can install drivers (dbc uses /tmp)
RUN apk --update add ca-certificates && \
  mkdir -p /tmp && \
  chmod 1777 /tmp

FROM scratch
COPY --from=base /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=base /tmp /tmp
ENTRYPOINT ["/dbc"]
COPY dbc /
