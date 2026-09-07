package storage

import (
	"context"
	"reflect"
)

type completeBackend interface {
	SignURL(context.Context, ObjectTarget, AccessOptions) (Access, error)
	SignDownloadPart(context.Context, ObjectTarget, ByteRange, AccessOptions) (Access, error)
	InitMultipartUpload(context.Context, ObjectTarget) (UploadID, error)
	SignMultipartPart(context.Context, MultipartPartRequest) (Access, error)
	CompleteMultipartUpload(context.Context, CompleteMultipartRequest) error
}

type invalidatingBackend interface {
	InvalidateBucket(string)
}

type probingBackend interface {
	Probe(context.Context, []ProbeTarget) []ProbeResult
}

type inventoryBackend interface {
	Inventory(context.Context, InventoryRequest) (InventoryResult, error)
}

type deletingBackend interface {
	Delete(context.Context, []PhysicalTarget) error
}

type Registration struct {
	provider    string
	complete    completeBackend
	invalidator invalidatingBackend
	prober      probingBackend
	inventory   inventoryBackend
	deleter     deletingBackend
}

func NewRegistration(provider string, backend completeBackend) Registration {
	registration := Registration{provider: provider, complete: backend}
	if isNilInterface(backend) {
		return registration
	}
	if invalidator, ok := backend.(invalidatingBackend); ok && !isNilInterface(invalidator) {
		registration.invalidator = invalidator
	}
	if prober, ok := backend.(probingBackend); ok && !isNilInterface(prober) {
		registration.prober = prober
	}
	if inventory, ok := backend.(inventoryBackend); ok && !isNilInterface(inventory) {
		registration.inventory = inventory
	}
	if deleter, ok := backend.(deletingBackend); ok && !isNilInterface(deleter) {
		registration.deleter = deleter
	}
	return registration
}

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
