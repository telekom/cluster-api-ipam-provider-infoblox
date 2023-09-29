#!/bin/bash
# Copyright 2020 The Kubernetes Authors.
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

prefix="CAIP_INFOBLOX_TEST_"
export `echo $prefix`HOST="192.168.114.4"
export `echo $prefix`SKIP_TLS_VERIFY="true"
export `echo $prefix`WAPI_VERSION="2.11"
export `echo $prefix`USERNAME="admin"
export `echo $prefix`PASSWORD="infoblox"
