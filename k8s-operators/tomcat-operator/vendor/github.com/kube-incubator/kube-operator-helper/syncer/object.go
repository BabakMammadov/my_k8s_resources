/*
Copyright 2018 Pressinfra SRL.

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

package syncer

import (
	"context"
	"fmt"

	"github.com/go-test/deep"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var (
	errOwnerDeleted = fmt.Errorf("owner is deleted")
)

// ObjectSyncer is a syncer.Interface for syncing kubernetes.Objects only by
// passing a SyncFn
type ObjectSyncer struct {
	Owner          runtime.Object
	Obj            runtime.Object
	SyncFn         controllerutil.MutateFn
	Name           string
	Client         client.Client
	Scheme         *runtime.Scheme
	previousObject runtime.Object
}

// GetObject returns the ObjectSyncer subject
func (s *ObjectSyncer) GetObject() interface{} { return s.Obj }

// GetOwner returns the ObjectSyncer owner
func (s *ObjectSyncer) GetOwner() runtime.Object { return s.Owner }

// Sync does the actual syncing and implements the syncer.Inteface Sync method
func (s *ObjectSyncer) Sync(ctx context.Context) (SyncResult, error) {
	result := SyncResult{}

	key, err := getKey(s.Obj)
	if err != nil {
		return result, err
	}

	result.Operation, err = controllerutil.CreateOrUpdate(ctx, s.Client, s.Obj, s.mutateFn())

	// check deep diff
	diff := deep.Equal(s.previousObject, s.Obj)

	// don't pass to user error for owner deletion, just don't create the object
	if err == errOwnerDeleted {
		log.Info(string(result.Operation), "key", key, "kind", fmt.Sprintf("%T", s.Obj), "error", err)
		err = nil
	} else if err != nil {
		result.SetEventData(eventWarning, basicEventReason(s.Name, err),
			fmt.Sprintf("%T %s failed syncing: %s", s.Obj, key, err))
		log.Error(err, string(result.Operation), "key", key, "kind", fmt.Sprintf("%T", s.Obj), "diff", diff)
	} else {
		result.SetEventData(eventNormal, basicEventReason(s.Name, err),
			fmt.Sprintf("%T %s %s successfully", s.Obj, key, result.Operation))
		log.V(1).Info(string(result.Operation), "key", key, "kind", fmt.Sprintf("%T", s.Obj), "diff", diff)
	}

	return result, err
}

// Given an ObjectSyncer, returns a controllerutil.MutateFn which also sets the
// owner reference if the subject has one
func (s *ObjectSyncer) mutateFn() controllerutil.MutateFn {
	return func(existing runtime.Object) error {
		s.previousObject = existing.DeepCopyObject()
		err := s.SyncFn(existing)
		if err != nil {
			return err
		}
		if s.Owner != nil {
			existingMeta, ok := existing.(metav1.Object)
			if !ok {
				return fmt.Errorf("%T is not a metav1.Object", existing)
			}
			ownerMeta, ok := s.Owner.(metav1.Object)
			if !ok {
				return fmt.Errorf("%T is not a metav1.Object", s.Owner)
			}

			// set owner reference only if owner resource is not being deleted, otherwise the owner
			// reference will be reset in case of deleting with cascade=false.
			if ownerMeta.GetDeletionTimestamp().IsZero() {
				err := controllerutil.SetControllerReference(ownerMeta, existingMeta, s.Scheme)
				if err != nil {
					return err
				}
			} else if ctime := existingMeta.GetCreationTimestamp(); ctime.IsZero() {
				// the owner is deleted, don't recreate the resource if does not exist, because gc
				// will not delete it again because has no owner reference set
				return errOwnerDeleted
			}
		}
		return nil
	}
}

// NewObjectSyncer creates a new kubernetes.Object syncer for a given object
// with an owner and persists data using controller-runtime's CreateOrUpdate.
// The name is used for logging and event emitting purposes and should be an
// valid go identifier in upper camel case. (eg. MysqlStatefulSet)
func NewObjectSyncer(name string, owner, obj runtime.Object, c client.Client, scheme *runtime.Scheme, syncFn controllerutil.MutateFn) Interface {
	return &ObjectSyncer{
		Owner:  owner,
		Obj:    obj,
		SyncFn: syncFn,
		Name:   name,
		Client: c,
		Scheme: scheme,
	}
}
