package storage

import (
	"context"
	"reflect"
)

type Provider interface {
	Sign(context.Context, ProviderBinding, SignRequest) (SignedAccess, error)
	BeginMultipart(context.Context, ProviderBinding, Target) (UploadID, error)
	SignMultipartPart(context.Context, ProviderBinding, MultipartPartRequest) (SignedAccess, error)
	CompleteMultipart(context.Context, ProviderBinding, CompleteMultipartRequest) error
}

type Invalidator interface {
	InvalidateBucket(string)
}

type Prober interface {
	Probe(context.Context, ProviderBinding, []ProbeTarget) []ProbeResult
}

type Inventoryer interface {
	Inventory(context.Context, ProviderBinding, InventoryRequest) (InventoryResult, error)
}

type Deleter interface {
	Delete(context.Context, ProviderBinding, []PhysicalTarget) error
}

type Registration struct {
	provider    string
	complete    Provider
	invalidator Invalidator
	prober      Prober
	inventory   Inventoryer
	deleter     Deleter
}

func NewRegistration(provider string, backend Provider) Registration {
	registration := Registration{provider: provider, complete: backend}
	if isNilInterface(backend) {
		return registration
	}
	if invalidator, ok := backend.(Invalidator); ok && !isNilInterface(invalidator) {
		registration.invalidator = invalidator
	}
	if prober, ok := backend.(Prober); ok && !isNilInterface(prober) {
		registration.prober = prober
	}
	if inventory, ok := backend.(Inventoryer); ok && !isNilInterface(inventory) {
		registration.inventory = inventory
	}
	if deleter, ok := backend.(Deleter); ok && !isNilInterface(deleter) {
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
