/*


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

package controllers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/openshift/library-go/pkg/manifest"
	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	operatorv1alpha1 "github.com/font/gatekeeper-operator/api/v1alpha1"
	"github.com/font/gatekeeper-operator/controllers/merge"
	"github.com/font/gatekeeper-operator/pkg/bindata"
)

var (
	defaultGatekeeperCrName = "gatekeeper"
	staticAssetsDir         = "config/gatekeeper/"
	orderedStaticAssets     = []string{
		"apiextensions.k8s.io_v1beta1_customresourcedefinition_configs.config.gatekeeper.sh.yaml",
		"apiextensions.k8s.io_v1beta1_customresourcedefinition_constrainttemplates.templates.gatekeeper.sh.yaml",
		"apiextensions.k8s.io_v1beta1_customresourcedefinition_constrainttemplatepodstatuses.status.gatekeeper.sh.yaml",
		"apiextensions.k8s.io_v1beta1_customresourcedefinition_constraintpodstatuses.status.gatekeeper.sh.yaml",
		"~g_v1_secret_gatekeeper-webhook-server-cert.yaml",
		"~g_v1_serviceaccount_gatekeeper-admin.yaml",
		"rbac.authorization.k8s.io_v1_clusterrole_gatekeeper-manager-role.yaml",
		"rbac.authorization.k8s.io_v1_clusterrolebinding_gatekeeper-manager-rolebinding.yaml",
		"rbac.authorization.k8s.io_v1_role_gatekeeper-manager-role.yaml",
		"rbac.authorization.k8s.io_v1_rolebinding_gatekeeper-manager-rolebinding.yaml",
		"apps_v1_deployment_gatekeeper-audit.yaml",
		"apps_v1_deployment_gatekeeper-controller-manager.yaml",
		"~g_v1_service_gatekeeper-webhook-service.yaml",
		"admissionregistration.k8s.io_v1beta1_validatingwebhookconfiguration_gatekeeper-validating-webhook-configuration.yaml",
	}
)

// GatekeeperReconciler reconciles a Gatekeeper object
type GatekeeperReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// Gatekeeper Operator RBAC permissions to manager Gatekeeper custom resource
// +kubebuilder:rbac:groups=operator.gatekeeper.sh,namespace="system",resources=gatekeepers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=operator.gatekeeper.sh,namespace="system",resources=gatekeepers/status,verbs=get;update;patch

// Gatekeeper Operator RBAC permissions to deploy Gatekeeper. Many of these RBAC permissions are needed because the operator must have the permissions to grant Gatekeeper its required RBAC permissions.
// Cluster Scoped
// +kubebuilder:rbac:groups=*,resources=*,verbs=get;list;watch
// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=config.gatekeeper.sh,resources=configs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=config.gatekeeper.sh,resources=configs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=constraints.gatekeeper.sh,resources=*,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=policy,resources=podsecuritypolicies,verbs=use
// +kubebuilder:rbac:groups=status.gatekeeper.sh,resources=*,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=templates.gatekeeper.sh,resources=constrainttemplates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=templates.gatekeeper.sh,resources=constrainttemplates/finalizers,verbs=get;update;patch;delete
// +kubebuilder:rbac:groups=templates.gatekeeper.sh,resources=constrainttemplates/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles;clusterrolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=validatingwebhookconfigurations,verbs=get;list;watch;create;update;patch;delete

// Namespace Scoped
// +kubebuilder:rbac:groups=core,namespace="system",resources=secrets;serviceaccounts;services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,namespace="system",resources=roles;rolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,namespace="system",resources=deployments,verbs=get;list;watch;create;update;patch;delete

func (r *GatekeeperReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	logger := r.Log.WithValues("gatekeeper", req.NamespacedName)
	logger.Info("Reconciling Gatekeeper")

	if req.Name != defaultGatekeeperCrName {
		err := fmt.Errorf("Gatekeeper resource name must be '%s'", defaultGatekeeperCrName)
		logger.Error(err, "Invalid Gatekeeper resource name")
		// Return success to avoid requeue
		return ctrl.Result{}, nil
	}

	gatekeeper := &operatorv1alpha1.Gatekeeper{}
	err := r.Get(ctx, req.NamespacedName, gatekeeper)
	if err != nil {
		// Reconcile failed due to error - requeue
		return ctrl.Result{}, err
	}

	err = r.deployGatekeeperResources()
	if err != nil {
		return ctrl.Result{}, errors.Wrap(err, "Unable to deploy Gatekeeper resources")
	}

	return ctrl.Result{}, nil
}

func (r *GatekeeperReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&operatorv1alpha1.Gatekeeper{}).
		Complete(r)
}

func (r *GatekeeperReconciler) deployGatekeeperResources() error {
	ctx := context.Background()
	var err error
	for _, a := range orderedStaticAssets {
		assetName := staticAssetsDir + a
		bytes, err := bindata.Asset(assetName)
		if err != nil {
			return err
		}

		logger := r.Log.WithValues("Gatekeeper resource", string(assetName))
		manifest := &manifest.Manifest{}
		err = manifest.UnmarshalJSON(bytes)
		if err != nil {
			return err
		}

		err = r.Create(ctx, manifest.Obj)
		if err == nil {
			logger.Info(fmt.Sprintf("Created Gatekeeper resource"))
			continue
		}

		// Create returned an error, now process it.

		if !apierrors.IsAlreadyExists(err) {
			return err
		}

		clusterObj := &unstructured.Unstructured{}
		clusterObj.SetAPIVersion(manifest.Obj.GetAPIVersion())
		clusterObj.SetKind(manifest.Obj.GetKind())
		namespacedName := types.NamespacedName{
			Namespace: manifest.Obj.GetNamespace(),
			Name:      manifest.Obj.GetName(),
		}
		err = r.Get(ctx, namespacedName, clusterObj)
		if err != nil {
			return err
		}

		err = merge.RetainClusterObjectFields(manifest.Obj, clusterObj)
		if err != nil {
			return errors.Wrapf(err, "Unable to retain cluster object fields from %s", namespacedName)
		}

		err = r.Update(ctx, manifest.Obj)
		if err != nil {
			return err
		}

		logger.Info(fmt.Sprintf("Updated Gatekeeper resource"))
	}

	return err
}
