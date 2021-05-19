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
	"testing"
)

func TestInitEx(t *testing.T) {
	// Set up
	path := os.Getenv("LIBPATH")
	if path == "" {
		t.Skip("Skipping testing if environment variables are not set.")
	}
	config := os.Getenv("CONFIGFILE")
	if config == "" {
		t.Skip("Skipping testing if environment variables are not set.")
	}
	res := disklib.InitEx(7, 0, path, config)
	if res != nil {
		t.Errorf("Init failed, got error code: %d, error message: %s.", res.VixErrorCode(), res.Error())
	}
	serverName := os.Getenv("IP")
	thumPrint := os.Getenv("THUMBPRINT")
	userName := os.Getenv("USERNAME")
	password := os.Getenv("PASSWORD")
	fcdId := os.Getenv("FCDID")
	ds := os.Getenv("DATASTORE")
	identity := os.Getenv("IDENTITY")
	params := disklib.NewConnectParams("", serverName,thumPrint, userName,
		password, fcdId, ds, "", "", identity, "", disklib.VIXDISKLIB_FLAG_OPEN_COMPRESSION_SKIPZ,
		false, disklib.NBD)
	err1 := disklib.PrepareForAccess(params)
	if err1 != nil {
		t.Errorf("Prepare for access failed. Error code: %d. Error message: %s.", err1.VixErrorCode(), err1.Error())
	}
	conn, err2 := disklib.ConnectEx(params)
	if err2 != nil {
		disklib.EndAccess(params)
		t.Errorf("Connect to vixdisk lib failed. Error code: %d. Error message: %s.", err2.VixErrorCode(), err2.Error())
	}
	disklib.Disconnect(conn)
	disklib.EndAccess(params)
}
