package update

import (
	"fmt"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const upgradeCode = "{92E48FC1-263E-4F72-87C4-D23E3B10FA23}"

var (
	msi          = windows.NewLazySystemDLL("msi.dll")
	openDB       = msi.NewProc("MsiOpenDatabaseW")
	openView     = msi.NewProc("MsiDatabaseOpenViewW")
	viewExecute  = msi.NewProc("MsiViewExecute")
	viewFetch    = msi.NewProc("MsiViewFetch")
	recordString = msi.NewProc("MsiRecordGetStringW")
	closeHandle  = msi.NewProc("MsiCloseHandle")
	enumRelated  = msi.NewProc("MsiEnumRelatedProductsW")
	productInfo  = msi.NewProc("MsiGetProductInfoW")
)

func msiCall(name string, proc *windows.LazyProc, args ...uintptr) error {
	r, _, _ := proc.Call(args...)
	if r != 0 {
		return fmt.Errorf("%s failed: Windows Installer error %d", name, r)
	}
	return nil
}

func installedProductVersion() (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	code, _ := windows.UTF16PtrFromString(upgradeCode)
	product := make([]uint16, 39)
	if err := msiCall("MsiEnumRelatedProducts", enumRelated, uintptr(unsafe.Pointer(code)), 0, 0, uintptr(unsafe.Pointer(&product[0]))); err != nil {
		return "", err
	}
	second := make([]uint16, 39)
	r, _, _ := enumRelated.Call(uintptr(unsafe.Pointer(code)), 0, 1, uintptr(unsafe.Pointer(&second[0])))
	if r != uintptr(windows.ERROR_NO_MORE_ITEMS) {
		if r == 0 {
			return "", fmt.Errorf("multiple related NekoSimulator233 products are installed")
		}
		return "", fmt.Errorf("MsiEnumRelatedProducts failed: Windows Installer error %d", r)
	}
	prop, _ := windows.UTF16PtrFromString("VersionString")
	value := make([]uint16, 256)
	length := uint32(len(value))
	if err := msiCall("MsiGetProductInfo", productInfo, uintptr(unsafe.Pointer(&product[0])), uintptr(unsafe.Pointer(prop)), uintptr(unsafe.Pointer(&value[0])), uintptr(unsafe.Pointer(&length))); err != nil {
		return "", err
	}
	return windows.UTF16ToString(value), nil
}

func property(db uintptr, name string) (string, error) {
	query, err := windows.UTF16PtrFromString("SELECT `Value` FROM `Property` WHERE `Property` = '" + name + "'")
	if err != nil {
		return "", err
	}
	var view, record uintptr
	if err = msiCall("MsiDatabaseOpenView", openView, db, uintptr(unsafe.Pointer(query)), uintptr(unsafe.Pointer(&view))); err != nil {
		return "", err
	}
	defer closeHandle.Call(view)
	if err = msiCall("MsiViewExecute", viewExecute, view, 0); err != nil {
		return "", err
	}
	if err = msiCall("MsiViewFetch", viewFetch, view, uintptr(unsafe.Pointer(&record))); err != nil {
		return "", err
	}
	defer closeHandle.Call(record)
	buf := make([]uint16, 1024)
	length := uint32(len(buf))
	if err = msiCall("MsiRecordGetString", recordString, record, 1, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&length))); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf), nil
}

// VerifyPackage checks package identity and the version stated by the local manifest.
// Hash and package metadata detect mistakes but do not authenticate the publisher.
func VerifyPackage(path, expectedVersion string) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	var db uintptr
	if err = msiCall("MsiOpenDatabase", openDB, uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&db))); err != nil {
		return err
	}
	defer closeHandle.Call(db)
	for name, expected := range map[string]string{"UpgradeCode": upgradeCode, "ProductName": "NekoSimulator233", "ProductVersion": expectedVersion} {
		got, err := property(db, name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		if !strings.EqualFold(got, expected) {
			return fmt.Errorf("unexpected MSI %s: %s", name, got)
		}
	}
	return nil
}
