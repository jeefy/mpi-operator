/*
Copyright 2018 The Kubernetes authors.

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

package mpi

import (
	"testing"
	"time"

	schedulingv1alpha1 "github.com/jeefy/mpi-operator/pkg/apis/scheduling/v1alpha1"
	arbcorev1 "github.com/kubernetes-sigs/kube-batch/pkg/apis/scheduling/v1alpha1"
	"github.com/onsi/gomega"
	"golang.org/x/net/context"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var c client.Client

var expectedRequest = reconcile.Request{NamespacedName: types.NamespacedName{Name: "foo", Namespace: "default"}}
var jobKey = types.NamespacedName{Name: "foo", Namespace: "default"}
var pgKey = types.NamespacedName{Name: "foo-podgroup", Namespace: "default"}

const timeout = time.Second * 10

func TestReconcile(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	instance := &schedulingv1alpha1.MPI{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: schedulingv1alpha1.MPISpec{
			Job: batchv1.Job{
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								corev1.Container{
									Image: "busybox",
									Name:  "test",
								},
							},
						},
					},
				},
			},
			PodGroup: arbcorev1.PodGroup{
				Spec: arbcorev1.PodGroupSpec{
					MinMember: 1,
				},
			},
		},
	}

	hostfileConfigData := map[string]string{}
	hostfileConfigData["hostfile"] = ""

	hostfileConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "foo-hostfile",
			Namespace: "default",
		},
		Data: hostfileConfigData,
	}

	mpiConfigData := map[string]string{}
	mpiConfigData["thing"] = ""

	mpiConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "foo-mpi-data",
			Namespace: "default",
		},
		Data: mpiConfigData,
	}

	// Setup the Manager and Controller.  Wrap the Controller Reconcile function so it writes each request to a
	// channel when it is finished.
	mgr, err := manager.New(cfg, manager.Options{})
	g.Expect(err).NotTo(gomega.HaveOccurred())
	c = mgr.GetClient()

	recFn, requests := SetupTestReconcile(newReconciler(mgr))
	g.Expect(add(mgr, recFn)).NotTo(gomega.HaveOccurred())

	stopMgr, mgrStopped := StartTestManager(mgr, g)

	defer func() {
		close(stopMgr)
		mgrStopped.Wait()
	}()

	// We need that hostfile configmap
	err = c.Create(context.TODO(), hostfileConfigMap)
	if apierrors.IsInvalid(err) {
		t.Logf("failed to create configmap, got an invalid object error: %v", err)
		return
	}

	err = c.Create(context.TODO(), mpiConfigMap)
	if apierrors.IsInvalid(err) {
		t.Logf("failed to create configmap, got an invalid object error: %v", err)
		return
	}

	// Create the MPI object and expect the Reconcile and Job to be created
	err = c.Create(context.TODO(), instance)
	// The instance object may not be a valid object because it might be missing some required fields.
	// Please modify the instance object by adding required fields and then remove the following if statement.
	if apierrors.IsInvalid(err) {
		t.Logf("failed to create object, got an invalid object error: %v", err)
		return
	}
	g.Expect(err).NotTo(gomega.HaveOccurred())
	defer c.Delete(context.TODO(), instance)
	g.Eventually(requests, timeout).Should(gomega.Receive(gomega.Equal(expectedRequest)))

	job := &batchv1.Job{}
	g.Eventually(func() error { return c.Get(context.TODO(), jobKey, job) }, timeout).
		Should(gomega.Succeed())

	// Delete the Job and expect Reconcile to be called for Job deletion
	g.Expect(c.Delete(context.TODO(), job)).NotTo(gomega.HaveOccurred())
	g.Eventually(requests, timeout).Should(gomega.Receive(gomega.Equal(expectedRequest)))
	g.Eventually(func() error { return c.Get(context.TODO(), jobKey, job) }, timeout).
		Should(gomega.Succeed())

	podGroup := &arbcorev1.PodGroup{}
	g.Eventually(func() error { return c.Get(context.TODO(), pgKey, podGroup) }, timeout).
		Should(gomega.Succeed())

	// Delete the PodGroup and expect Reconcile to be called for PodGroup deletion
	g.Expect(c.Delete(context.TODO(), podGroup)).NotTo(gomega.HaveOccurred())
	g.Eventually(requests, timeout).Should(gomega.Receive(gomega.Equal(expectedRequest)))
	g.Eventually(func() error { return c.Get(context.TODO(), pgKey, podGroup) }, timeout).
		Should(gomega.Succeed())

	// Manually delete Job since GC isn't enabled in the test control plane
	g.Expect(c.Delete(context.TODO(), job)).To(gomega.Succeed())
	// Manually delete PodGroup since GC isn't enabled in the test control plane
	g.Expect(c.Delete(context.TODO(), podGroup)).To(gomega.Succeed())
	// Manually delete Job since GC isn't enabled in the test control plane
	g.Expect(c.Delete(context.TODO(), mpiConfigMap)).To(gomega.Succeed())
	// Manually delete Job since GC isn't enabled in the test control plane
	g.Expect(c.Delete(context.TODO(), hostfileConfigMap)).To(gomega.Succeed())

}
