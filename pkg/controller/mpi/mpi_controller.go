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
	"context"
	"fmt"
	"log"
	"reflect"

	schedulingv1alpha1 "github.com/jeefy/mpi-operator/pkg/apis/scheduling/v1alpha1"
	arbcorev1 "github.com/kubernetes-sigs/kube-batch/pkg/apis/scheduling/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/jeefy/mpi-operator/pkg/util"
)

const kubeexecsh = `#!/bin/sh
echo "kubectl executor starting for $1"
set -x
POD_NAME=$1
shift
/usr/bin/kubectl exec ${POD_NAME} -- /bin/sh -c "cat /entry/hosts >> /etc/hosts"
/usr/bin/kubectl exec ${POD_NAME} -- /bin/sh -c "$*"
/usr/bin/kubectl exec ${POD_NAME} -- /bin/sh -c "echo $? > /tmp/exit"`

const startupsh = `#!/bin/sh
while true; do
	if [ "$(cat /entry/executor)" = "$(hostname)" ]; then
		echo "Look at me. I'm the executor now.";
		break
	fi
	if [ -f /tmp/exit ]; then
		exit $(cat /tmp/exit);
	fi
	sleep 1
done
wget https://storage.googleapis.com/kubernetes-release/release/v1.12.2/bin/linux/amd64/kubectl && \
mv kubectl /usr/bin/kubectl && \
chmod +x /usr/bin/kubectl

cat /entry/hosts >> /etc/hosts

echo "Time to do the thing: $*"
$*`

/**
* USER ACTION REQUIRED: This is a scaffold file intended for the user to modify with their own Controller
* business logic.  Delete these comments after modifying this file.*
 */

// Add creates a new MPI Controller and adds it to the Manager with default RBAC. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
// USER ACTION REQUIRED: update cmd/manager/main.go to call this scheduling.Add(mgr) to install this Controller
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileMPI{Client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("mpi-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to MPI
	err = c.Watch(&source.Kind{Type: &schedulingv1alpha1.MPI{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// Watch for changes to PodGroup
	err = c.Watch(&source.Kind{Type: &arbcorev1.PodGroup{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &schedulingv1alpha1.MPI{},
	})
	if err != nil {
		return err
	}

	// Watch a Job created by MPI
	err = c.Watch(&source.Kind{Type: &batchv1.Job{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &schedulingv1alpha1.MPI{},
	})
	if err != nil {
		return err
	}

	// Watch a Job created by MPI
	err = c.Watch(&source.Kind{Type: &corev1.ConfigMap{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &schedulingv1alpha1.MPI{},
	})
	if err != nil {
		return err
	}

	clientset, err := kubernetes.NewForConfig(mgr.GetConfig())
	if err != nil {
		return err
	}

	watcher, err := clientset.Core().Pods("").Watch(metav1.ListOptions{
		LabelSelector: "job-type=mpi-job",
	})
	if err != nil {
		return err
	}

	go func() {
		for {
			select {
			case e := <-watcher.ResultChan():
				if e.Object == nil {
					return
				}

				pod, ok := e.Object.(*corev1.Pod)
				if !ok {
					continue
				}

				//fmt.Printf("%v on %v/%v - %v (%v)\n", e.Type, pod.Namespace, pod.Name, pod.Status.Phase, pod.Status.Reason)

				switch e.Type {
				case watch.Modified:
					if pod.DeletionTimestamp != nil {
						continue
					}

					switch pod.Status.Phase {
					case corev1.PodRunning:
						pods, err := clientset.CoreV1().Pods(pod.GetNamespace()).List(metav1.ListOptions{
							LabelSelector: "job-name=" + pod.GetLabels()["job-name"],
						})
						if err != nil {
							fmt.Printf("Error getting a pod list for mpi-job %v\n", pod.GetLabels()["job-name"])
						}
						ipMap := make(map[string]string)
						hostMap := []string{}
						hostfileString := ""
						etcHostsString := ""
						for _, v := range pods.Items {
							if v.Status.PodIP != "" {
								ipMap[v.GetName()] = v.Status.PodIP
								hostMap = append(hostMap, v.GetName())
								hostfileString = hostfileString + v.GetName() + "\n"
								etcHostsString = etcHostsString + v.Status.PodIP + " " + v.GetName() + "\n"
							}
						}
						if len(ipMap) == len(pods.Items) {

							hostfileMapName := pod.GetLabels()["job-name"] + "-hostfile"
							dataMapName := pod.GetLabels()["job-name"] + "-mpi-data"
							fmt.Printf("We have IPs, time to update %v\n", hostfileMapName)
							fmt.Printf("%v\n", ipMap)
							clientset.CoreV1().ConfigMaps(pod.GetNamespace()).Update(
								&corev1.ConfigMap{
									ObjectMeta: metav1.ObjectMeta{
										Name:      hostfileMapName,
										Namespace: pod.GetNamespace(),
									},
									Data: map[string]string{
										"hostfile": hostfileString,
									},
								},
							)
							fmt.Printf("Our executor is now %v\n", pods.Items[0].Name)
							clientset.CoreV1().ConfigMaps(pod.GetNamespace()).Update(
								&corev1.ConfigMap{
									ObjectMeta: metav1.ObjectMeta{
										Name:      dataMapName,
										Namespace: pod.GetNamespace(),
									},
									Data: map[string]string{
										"executor":    pods.Items[0].Name,
										"kubeexec.sh": kubeexecsh,
										"startup.sh":  startupsh,
										"hosts":       etcHostsString,
									},
								},
							)

							role, err := clientset.RbacV1().Roles(pod.GetNamespace()).Get(pod.GetLabels()["job-name"]+"-role", metav1.GetOptions{})
							if err != nil {
								fmt.Printf("Error getting role %s", pod.GetLabels()["job-name"]+"-role")
							}
							for k, _ := range role.Rules {
								role.Rules[k].ResourceNames = hostMap
							}
							clientset.RbacV1().Roles(pod.GetNamespace()).Update(role)
						}

						break
					default:
						break
					}
				}
			}
		}
	}()
	return nil
}

var _ reconcile.Reconciler = &ReconcileMPI{}

// ReconcileMPI reconciles a MPI object
type ReconcileMPI struct {
	client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a MPI object and makes changes based on the state read
// and what is in the MPI.Spec

// Automatically generate RBAC rules to allow the Controller to read and write Jobs and Pods and PodGroups
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods/exec,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=scheduling.incubator.k8s.io,resources=podgroups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=scheduling.incubator.k8s.io,resources=mpis,verbs=get;list;watch;create;update;patch;delete
func (r *ReconcileMPI) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	// Fetch the MPI instance
	instance := &schedulingv1alpha1.MPI{}
	err := r.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Object not found, return.  Created objects are automatically garbage collected.
			// For additional cleanup logic use finalizers.
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	gvk, err := apiutil.GVKForObject(instance, r.scheme)
	if err != nil {
		return reconcile.Result{}, err
	}

	// Create all the RBAC.
	sa := generateServiceAccount(instance, gvk)
	foundSA := &corev1.ServiceAccount{}
	err = r.Get(context.TODO(), types.NamespacedName{Name: sa.GetName(), Namespace: sa.GetNamespace()}, foundSA)
	if err != nil && errors.IsNotFound(err) {
		log.Printf("Creating SA %s/%s\n", sa.GetNamespace(), sa.GetName())
		err = r.Create(context.TODO(), sa)
		if err != nil {
			log.Printf("Error creating SA %s/%s - %v\n", sa.GetNamespace(), sa.GetName(), err.Error())
			return reconcile.Result{}, err
		}
	} else if err != nil {
		log.Printf("Error getting SA %s/%s - %v\n", sa.GetNamespace(), sa.GetName(), err.Error())
		return reconcile.Result{}, err
	}

	role := generateRole(instance, gvk)
	foundRole := &rbacv1.Role{}
	err = r.Get(context.TODO(), types.NamespacedName{Name: role.GetName(), Namespace: role.GetNamespace()}, foundRole)
	if err != nil && errors.IsNotFound(err) {
		log.Printf("Creating Role %s/%s\n", role.GetNamespace(), role.GetName())
		err = r.Create(context.TODO(), role)
		if err != nil {
			log.Printf("Error creating Role %s/%s - %v\n", role.GetNamespace(), role.GetName(), err.Error())
			return reconcile.Result{}, err
		}
	} else if err != nil {
		log.Printf("Error getting Role %s/%s - %v\n", role.GetNamespace(), role.GetName(), err.Error())
		return reconcile.Result{}, err
	}

	roleBinding := generateRoleBinding(instance, gvk)
	foundRoleBinding := &rbacv1.RoleBinding{}
	err = r.Get(context.TODO(), types.NamespacedName{Name: roleBinding.GetName(), Namespace: roleBinding.GetNamespace()}, foundRoleBinding)
	if err != nil && errors.IsNotFound(err) {
		log.Printf("Creating RoleBinding %s/%s\n", roleBinding.GetNamespace(), roleBinding.GetName())
		err = r.Create(context.TODO(), roleBinding)
		if err != nil {
			log.Printf("Error creating RoleBinding %s/%s - %v\n", roleBinding.GetNamespace(), roleBinding.GetName(), err.Error())
			return reconcile.Result{}, err
		}
	} else if err != nil {
		log.Printf("Error getting RoleBinding %s/%s - %v\n", roleBinding.GetNamespace(), roleBinding.GetName(), err.Error())
		return reconcile.Result{}, err
	}

	// Check if the hostfile ConfigMap already exists
	foundHostfileCm := &corev1.ConfigMap{}
	err = r.Get(context.TODO(), types.NamespacedName{Name: instance.GetName() + "-hostfile", Namespace: instance.GetNamespace()}, foundHostfileCm)
	if err != nil && errors.IsNotFound(err) {
		log.Printf("Creating ConfigMap %s/%s\n", instance.GetNamespace(), instance.GetName()+"-hostfile")
		err = r.Create(context.TODO(), &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      instance.GetName() + "-hostfile",
				Namespace: instance.GetNamespace(),
				OwnerReferences: []metav1.OwnerReference{
					*metav1.NewControllerRef(instance, gvk),
				},
			},
			Data: map[string]string{
				"hostfile": "",
			},
		})
		if err != nil {
			log.Printf("Error creating ConfigMap %s/%s - %v\n", instance.GetNamespace(), instance.GetName()+"-hostfile", err.Error())
			return reconcile.Result{}, err
		}
	} else if err != nil {
		log.Printf("Error getting ConfigMap %s/%s - %v\n", instance.GetNamespace(), instance.GetName()+"-hostfile", err.Error())
		return reconcile.Result{}, err
	}

	// Check if the hostfile ConfigMap already exists
	foundMPIDataCm := &corev1.ConfigMap{}
	err = r.Get(context.TODO(), types.NamespacedName{Name: instance.GetName() + "-mpi-data", Namespace: instance.GetNamespace()}, foundMPIDataCm)
	if err != nil && errors.IsNotFound(err) {
		log.Printf("Creating ConfigMap %s/%s\n", instance.GetNamespace(), instance.GetName()+"-mpi-data")
		err = r.Create(context.TODO(), &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      instance.GetName() + "-mpi-data",
				Namespace: instance.GetNamespace(),
				OwnerReferences: []metav1.OwnerReference{
					*metav1.NewControllerRef(instance, gvk),
				},
			},
			Data: map[string]string{
				"executor":    "",
				"hosts":       "",
				"kubeexec.sh": kubeexecsh,
				"startup.sh":  startupsh,
			},
		})
		if err != nil {
			log.Printf("Error creating ConfigMap %s/%s - %v\n", instance.GetNamespace(), instance.GetName()+"-hostfile", err.Error())
			return reconcile.Result{}, err
		}
	} else if err != nil {
		log.Printf("Error getting ConfigMap %s/%s - %v\n", instance.GetNamespace(), instance.GetName()+"-hostfile", err.Error())
		return reconcile.Result{}, err
	}

	// Define the desired Job object
	job := util.GenerateJob(instance, instance.Spec.Job)
	if err := controllerutil.SetControllerReference(instance, job, r.scheme); err != nil {
		return reconcile.Result{}, err
	}

	// Define the desired PodGroup object
	podgroup := &arbcorev1.PodGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      instance.GetName() + "-podgroup",
			Namespace: instance.GetNamespace(),
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(instance, gvk),
			},
		},
		Spec: instance.Spec.PodGroup.Spec,
	}
	if err := controllerutil.SetControllerReference(instance, podgroup, r.scheme); err != nil {
		return reconcile.Result{}, err
	}

	// Check if the Job already exists
	foundJob := &batchv1.Job{}
	err = r.Get(context.TODO(), types.NamespacedName{Name: job.GetName(), Namespace: job.GetNamespace()}, foundJob)
	if err != nil && errors.IsNotFound(err) {
		log.Printf("Creating Job %s/%s\n", job.GetNamespace(), job.GetName())
		err = r.Create(context.TODO(), job)
		if err != nil {
			log.Printf("Error creating job %s/%s - %v\n", job.GetNamespace(), job.GetName(), err.Error())
			return reconcile.Result{}, err
		}
	} else if err != nil {
		log.Printf("Error getting job %s/%s - %v\n", job.GetNamespace(), job.GetName(), err.Error())
		return reconcile.Result{}, err
	}

	// Update the found object and write the result back if there are any changes
	if !reflect.DeepEqual(job.Spec, foundJob.Spec) {
		foundJob.Spec = job.Spec
		if foundJob.GetName() != "" && foundJob.GetUID() != "" {
			log.Printf("Updating Job %s/%s\n", job.GetNamespace(), job.GetName())
			err = r.Update(context.TODO(), foundJob)
			if err != nil {
				return reconcile.Result{}, err
			}
		}
	}

	// Check if the PodGroup already exists
	foundPodGroup := &arbcorev1.PodGroup{}
	err = r.Get(context.TODO(), types.NamespacedName{Name: podgroup.GetName(), Namespace: podgroup.GetNamespace()}, foundPodGroup)
	if err != nil && errors.IsNotFound(err) {
		log.Printf("Creating PodGroup %s/%s\n", podgroup.GetNamespace(), podgroup.GetName())
		err = r.Create(context.TODO(), podgroup)
		if err != nil {
			return reconcile.Result{}, err
		}
	} else if err != nil {
		return reconcile.Result{}, err
	}

	// Update the found object and write the result back if there are any changes
	if !reflect.DeepEqual(podgroup.Spec, foundPodGroup.Spec) {
		foundPodGroup.Spec = podgroup.Spec
		if foundPodGroup.GetName() != "" && foundPodGroup.GetUID() != "" {
			log.Printf("Updating PodGroup %s/%s\n", podgroup.GetNamespace(), podgroup.GetName())
			err = r.Update(context.TODO(), foundPodGroup)
			if err != nil {
				log.Printf("Error updating podGroup %s/%s - %s", job.GetNamespace(), job.GetName(), err.Error())
				return reconcile.Result{}, err
			}
		}
	}
	/*
		// name of your custom finalizer
		myFinalizerName := "finalizers.scheduling.incubator.k8s.io"

		if instance.ObjectMeta.DeletionTimestamp.IsZero() {
			// The object is not being deleted, so if it does not have our finalizer,
			// then lets add the finalizer and update the object.
			if !slice.ContainsString(instance.ObjectMeta.Finalizers, myFinalizerName, nil) {
				instance.ObjectMeta.Finalizers = append(instance.ObjectMeta.Finalizers, myFinalizerName)
				if err := r.Update(context.Background(), instance); err != nil {
					return reconcile.Result{Requeue: true}, nil
				}
			}
		} else {
			// The object is being deleted
			if slice.ContainsString(instance.ObjectMeta.Finalizers, myFinalizerName, nil) {
				// our finalizer is present, so lets handle our external dependency
				configmaps := []string{"-hostfile", "-mpi-data"}

				for _, v := range configmaps {
					staleConfigMap := &corev1.ConfigMap{}
					err = r.Get(context.TODO(), types.NamespacedName{Name: instance.GetName() + v, Namespace: instance.GetNamespace()}, staleConfigMap)
					if err != nil && errors.IsNotFound(err) {
						log.Printf("ConfigMap deletion failed! Configmap %v/%v doesn't exist!", instance.GetName()+v, instance.GetNamespace())
						return reconcile.Result{}, err
					}

					err = r.Delete(context.TODO(), staleConfigMap)
					if err != nil {
						log.Printf("ConfigMap deletion failed! Error deleting %v/%v - %v", instance.GetName()+v, instance.GetNamespace(), err.Error())
						return reconcile.Result{}, err
					}

					// remove our finalizer from the list and update it.
					instance.ObjectMeta.Finalizers = slice.RemoveString(instance.ObjectMeta.Finalizers, myFinalizerName, nil)
					if err := r.Update(context.Background(), instance); err != nil {
						return reconcile.Result{Requeue: true}, nil
					}
				}
			}

			// Our finalizer has finished, so the reconciler can do nothing.
		}
	*/
	return reconcile.Result{}, nil
}

func generateRole(mpi metav1.Object, gvk schema.GroupVersionKind) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mpi.GetName() + "-role",
			Namespace: mpi.GetNamespace(),
			Labels: map[string]string{
				"job-name": mpi.GetName(),
			},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(mpi, gvk),
			},
		},
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:         []string{"get"},
				APIGroups:     []string{""},
				Resources:     []string{"pods"},
				ResourceNames: []string{""},
			},
			{
				Verbs:         []string{"create"},
				APIGroups:     []string{""},
				Resources:     []string{"pods/exec"},
				ResourceNames: []string{""},
			},
		},
	}
}
func generateRoleBinding(mpi metav1.Object, gvk schema.GroupVersionKind) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mpi.GetName() + "-rolebinding",
			Namespace: mpi.GetNamespace(),
			Labels: map[string]string{
				"job-name": mpi.GetName(),
			},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(mpi, gvk),
			},
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      rbacv1.ServiceAccountKind,
				Name:      mpi.GetName() + "-sa",
				Namespace: mpi.GetNamespace(),
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     mpi.GetName() + "-role",
		},
	}
}

func generateServiceAccount(mpi metav1.Object, gvk schema.GroupVersionKind) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mpi.GetName() + "-sa",
			Namespace: mpi.GetNamespace(),
			Labels: map[string]string{
				"job-name": mpi.GetName(),
			},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(mpi, gvk),
			},
		},
	}
}
