# Copyright 2020 Cloud Run Docker Mirror Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

build-push:
	@podman buildx build \
		--platform linux/arm64 \
		--tag asia-docker.pkg.dev/vargolabs/cloud-run-docker-mirror/server:latest \
		--tag docker.pkg.github.com/sethvargo/cloud-run-docker-mirror/server:latest \
		--tag gcr.io/vargolabs/cloud-run-docker-mirror/server:latest \
		--tag us-docker.pkg.dev/vargolabs/cloud-run-docker-mirror/server:latest \
	  --tag europe-docker.pkg.dev/vargolabs/cloud-run-docker-mirror/server:latest \
		--push \
		.
.PHONY: build-push

test:
	@go test \
		-count=1 \
		-race \
		-shuffle=on \
		-timeout=15m \
		./...
.PHONY: test
