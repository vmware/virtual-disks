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

package virtual_disks

import "C"
import (
	"fmt"
	"io"
	"sync"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/vmware/virtual-disks/pkg/disklib"
)

func OpenFCD(serverName string, thumbPrint string, userName string, password string, fcdId string, fcdssid string, datastore string,
	flags uint32, readOnly bool, transportMode string, identity string, logger logrus.FieldLogger) (DiskReaderWriter, disklib.VddkError) {
	globalParams := disklib.NewConnectParams("",
		serverName,
		thumbPrint,
		userName,
		password,
		fcdId,
		datastore,
		fcdssid,
		"",
		identity,
		"",
		flags,
		readOnly,
		transportMode,
		"")
	return Open(globalParams, logger)
}

func Open(globalParams disklib.ConnectParams, logger logrus.FieldLogger) (DiskReaderWriter, disklib.VddkError) {
	err := disklib.PrepareForAccess(globalParams)
	if err != nil {
		return DiskReaderWriter{}, err
	}
	conn, err := disklib.ConnectEx(globalParams)
	if err != nil {
		disklib.EndAccess(globalParams)
		return DiskReaderWriter{}, err
	}
	dli, err := disklib.Open(conn, globalParams)
	if err != nil {
		disklib.Disconnect(conn)
		disklib.EndAccess(globalParams)
		return DiskReaderWriter{}, err
	}
	info, err := disklib.GetInfo(dli)
	if err != nil {
		disklib.Disconnect(conn)
		disklib.EndAccess(globalParams)
		return DiskReaderWriter{}, err
	}
	diskHandle := NewDiskHandle(dli, conn, globalParams, info)
	return NewDiskReaderWriter(diskHandle, logger), nil
}

type DiskReaderWriter struct {
	diskHandle DiskConnectHandle
	offset     *int64
	mutex      *sync.Mutex // Lock to ensure that multiple-threads do not break offset or see the same data twice
	logger     logrus.FieldLogger
}

func (this DiskReaderWriter) Read(p []byte) (n int, err error) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	bytesRead, err := this.diskHandle.ReadAt(p, *this.offset)
	*this.offset += int64(bytesRead)
	this.logger.Infof("Read returning %d, len(p) = %d, offset=%d\n", bytesRead, len(p), *this.offset)
	return bytesRead, err
}

func (this DiskReaderWriter) Write(p []byte) (n int, err error) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	bytesWritten, err := this.diskHandle.WriteAt(p, *this.offset)
	*this.offset += int64(bytesWritten)
	this.logger.Infof("Write returning %d, len(p) = %d, offset=%d\n", bytesWritten, len(p), *this.offset)
	return bytesWritten, err
}

func (this DiskReaderWriter) Seek(offset int64, whence int) (int64, error) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	desiredOffset := *this.offset
	switch whence {
	case io.SeekStart:
		desiredOffset = offset
	case io.SeekCurrent:
		desiredOffset += offset
	case io.SeekEnd:
		// Fix this later
		return *this.offset, errors.New("Seek from SeekEnd not implemented")
	}

	if desiredOffset < 0 {
		return 0, errors.New("Cannot seek to negative offset")
	}
	*this.offset = desiredOffset
	return *this.offset, nil
}

func (this DiskReaderWriter) ReadAt(p []byte, off int64) (n int, err error) {
	return this.diskHandle.ReadAt(p, off)
}

func (this DiskReaderWriter) WriteAt(p []byte, off int64) (n int, err error) {
	return this.diskHandle.WriteAt(p, off)
}

func (this DiskReaderWriter) Close() error {
	return this.diskHandle.Close()
}

func (this DiskReaderWriter) QueryAllocatedBlocks(startSector disklib.VixDiskLibSectorType, numSectors disklib.VixDiskLibSectorType, chunkSize disklib.VixDiskLibSectorType) ([]disklib.VixDiskLibBlock, disklib.VddkError) {
	return this.diskHandle.QueryAllocatedBlocks(startSector, numSectors, chunkSize)
}

func NewDiskReaderWriter(diskHandle DiskConnectHandle, logger logrus.FieldLogger) DiskReaderWriter {
	var offset int64
	offset = 0
	var mutex sync.Mutex
	retVal := DiskReaderWriter{
		diskHandle: diskHandle,
		offset:     &offset,
		mutex:      &mutex,
		logger:     logger,
	}
	return retVal
}

type DiskConnectHandle struct {
	mutex  *sync.Mutex
	dli    disklib.VixDiskLibHandle
	conn   disklib.VixDiskLibConnection
	params disklib.ConnectParams
	info   disklib.VixDiskLibInfo
}

func NewDiskHandle(dli disklib.VixDiskLibHandle, conn disklib.VixDiskLibConnection, params disklib.ConnectParams,
	info disklib.VixDiskLibInfo) DiskConnectHandle {
	var mutex sync.Mutex
	return DiskConnectHandle{
		mutex:  &mutex,
		dli:    dli,
		conn:   conn,
		params: params,
		info:   info,
	}
}

func mapError(vddkError disklib.VddkError) error {
	switch vddkError.VixErrorCode() {
	case disklib.VIX_E_DISK_OUTOFRANGE:
		return io.EOF
	default:
		return vddkError
	}
}

func aligned(len int, off int64) bool {
	return len%disklib.VIXDISKLIB_SECTOR_SIZE == 0 && off%disklib.VIXDISKLIB_SECTOR_SIZE == 0
}

func (this DiskConnectHandle) ReadAt(p []byte, off int64) (n int, err error) {
	capacity := this.Capacity()
	if off >= capacity {
		return 0, io.EOF
	}
	// If we're being asked for a read beyond the end of the disk, slice the buffer down
	if off+int64(len(p)) > capacity {
		readLen := int32(capacity - off)
		p = p[0:readLen]
	}
	startSector := off / disklib.VIXDISKLIB_SECTOR_SIZE
	var total int = 0

	if !aligned(len(p), off) {
		// Lock versus read and write of misaligned data so that read/modify/write cycle always gives correct
		// behavior (read/write is atomic even though misaligned)
		this.mutex.Lock()
		defer this.mutex.Unlock()
	}
	// Start missing aligned part
	if off%disklib.VIXDISKLIB_SECTOR_SIZE != 0 {
		tmpBuf := make([]byte, disklib.VIXDISKLIB_SECTOR_SIZE)
		err := disklib.Read(this.dli, (uint64)(startSector), 1, tmpBuf)
		if err != nil {
			return 0, mapError(err)
		}
		srcOff := int(off % disklib.VIXDISKLIB_SECTOR_SIZE)
		count := disklib.VIXDISKLIB_SECTOR_SIZE - srcOff
		if count > len(p) {
			count = len(p)
		}
		srcEnd := srcOff + count
		tmpSlice := tmpBuf[srcOff:srcEnd]
		copy(p[:count], tmpSlice)
		startSector = startSector + 1
		total = total + count
	}
	// Middle aligned part
	numAlignedSectors := (len(p) - total) / disklib.VIXDISKLIB_SECTOR_SIZE
	if numAlignedSectors > 0 {
		desOff := total
		desEnd := total + numAlignedSectors*disklib.VIXDISKLIB_SECTOR_SIZE
		err := disklib.Read(this.dli, (uint64)(startSector), (uint64)(numAlignedSectors), p[desOff:desEnd])
		if err != nil {
			return total, mapError(err)
		}
		startSector = startSector + int64(numAlignedSectors)
		total = total + numAlignedSectors*disklib.VIXDISKLIB_SECTOR_SIZE
	}
	// End missing aligned part
	if (len(p) - total) > 0 {
		tmpBuf := make([]byte, disklib.VIXDISKLIB_SECTOR_SIZE)
		err := disklib.Read(this.dli, (uint64)(startSector), 1, tmpBuf)
		if err != nil {
			return total, mapError(err)
		}
		count := len(p) - total
		srcEnd := count
		tmpSlice := tmpBuf[0:srcEnd]
		copy(p[total:], tmpSlice)
	}
	return total, nil
}

func (this DiskConnectHandle) WriteAt(p []byte, off int64) (n int, err error) {
	capacity := this.Capacity()
	// Just error if either the beginning or the end of the write extends beyond the end
	if off > capacity || off+int64(len(p)) > capacity {
		return 0, io.ErrShortWrite
	}

	if !aligned(len(p), off) {
		// Lock versus read and write of misaligned data so that read/modify/write cycle always gives correct
		// behavior (read/write is atomic even though misaligned)
		this.mutex.Lock()
		defer this.mutex.Unlock()
	}
	var total int64 = 0
	var srcOff int64 = 0 // start index for p to copy from
	var srcEnd int64 = 0
	startSector := off / disklib.VIXDISKLIB_SECTOR_SIZE
	// Start missing aligned part
	if off%disklib.VIXDISKLIB_SECTOR_SIZE != 0 {
		tmpBuf := make([]byte, disklib.VIXDISKLIB_SECTOR_SIZE)
		err := disklib.Read(this.dli, uint64(startSector), 1, tmpBuf)
		if err != nil {
			return 0, mapError(err)
		}
		desOff := off % disklib.VIXDISKLIB_SECTOR_SIZE
		count := disklib.VIXDISKLIB_SECTOR_SIZE - desOff
		if int64(len(p)) < count {
			count = int64(len(p))
		}
		desEnd := desOff + count
		srcEnd = srcOff + count
		copy(tmpBuf[desOff:desEnd], p[srcOff:srcEnd])
		err = disklib.Write(this.dli, uint64(startSector), 1, tmpBuf)
		if err != nil {
			return 0, mapError(err)
		}
		startSector = startSector + 1
		total = total + count
		srcOff = srcOff + count
	}
	// Middle aligned part, override directly
	if (int64(len(p))-total)/disklib.VIXDISKLIB_SECTOR_SIZE > 0 {
		numSector := (int64(len(p)) - total) / disklib.VIXDISKLIB_SECTOR_SIZE
		srcEnd = srcOff + numSector*disklib.VIXDISKLIB_SECTOR_SIZE
		err := disklib.Write(this.dli, uint64(startSector), uint64(numSector), p[srcOff:srcEnd])
		if err != nil {
			return int(total), mapError(err)
		}
		startSector = startSector + numSector
		total = total + numSector*disklib.VIXDISKLIB_SECTOR_SIZE
		srcOff = srcEnd
	}
	// End missing aligned part
	if int64(len(p))-total > 0 {
		count := int64(len(p)) - total
		srcEnd = srcOff + count
		tmpBuf := make([]byte, disklib.VIXDISKLIB_SECTOR_SIZE)
		err := disklib.Read(this.dli, uint64(startSector), 1, tmpBuf)
		if err != nil {
			return int(total), mapError(err)
		}
		copy(tmpBuf[:count], p[srcOff:srcEnd])
		err = disklib.Write(this.dli, uint64(startSector), 1, tmpBuf)
		if err != nil {
			return int(total), errors.Wrap(err, "Write into disk in part 3 failed part3.")
		}
	}
	return len(p), nil
}

func (this DiskConnectHandle) Close() error {
	vErr := disklib.Close(this.dli)
	if vErr != nil {
		return errors.New(fmt.Sprintf(vErr.Error()+" with error code: %d", vErr.VixErrorCode()))
	}

	vErr = disklib.Disconnect(this.conn)
	if vErr != nil {
		return errors.New(fmt.Sprintf(vErr.Error()+" with error code: %d", vErr.VixErrorCode()))
	}

	vErr = disklib.EndAccess(this.params)
	if vErr != nil {
		return errors.New(fmt.Sprintf(vErr.Error()+" with error code: %d", vErr.VixErrorCode()))
	}

	return nil
}

func (this DiskConnectHandle) Capacity() int64 {
	return int64(this.info.Capacity) * disklib.VIXDISKLIB_SECTOR_SIZE
}

// QueryAllocatedBlocks invokes the VDDK function of the same name.
func (this DiskConnectHandle) QueryAllocatedBlocks(startSector disklib.VixDiskLibSectorType, numSectors disklib.VixDiskLibSectorType, chunkSize disklib.VixDiskLibSectorType) ([]disklib.VixDiskLibBlock, disklib.VddkError) {
	return disklib.QueryAllocatedBlocks(this.dli, startSector, numSectors, chunkSize)
}
