/*
Copyright (c) 2018-2021 the Go Library for Virtual Disk Development Kit contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package main

import (
	"github.com/vmware/virtual-disks/pkg/disklib"
	"os"
	"strings"
	"testing"
)

func TestGetThumbPrintForServer(t *testing.T) {
	host := os.Getenv("HOST")
	port := os.Getenv("PORT")
	vmwareThumbprint := os.Getenv("THUMBPRINT")
	if vmwareThumbprint == "" {
		t.Skip("Skipping testing if environment variables are not set.")
	}
	thumbprint, err := disklib.GetThumbPrintForServer(host, port)
	if err != nil {
		t.Errorf("Thumbprint for %s:%s failed, err = %s\n", host, port, err)
	}
	t.Logf("Thumbprint for %s:%s is %s\n", host, port, thumbprint)
	if strings.Compare(vmwareThumbprint, thumbprint) != 0 {
		t.Errorf("Thumbprint %s does not match expected thumbprint %s for %s - check to see if cert has been updated at %s\n",
			thumbprint, vmwareThumbprint, host, host)
	}
}
