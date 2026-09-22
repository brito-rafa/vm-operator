// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package viadmin

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/vmware/govmomi/vapi/library"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	capiutil "sigs.k8s.io/cluster-api/util"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	clfixture "github.com/vmware-tanzu/vm-operator/test/e2e/infrastructure/vsphere/contentlibrary"
	"github.com/vmware-tanzu/vm-operator/test/e2e/infrastructure/vsphere/testbed"
	"github.com/vmware-tanzu/vm-operator/test/e2e/infrastructure/vsphere/vcenter"
	"github.com/vmware-tanzu/vm-operator/test/e2e/infrastructure/vsphere/wcp"
	"github.com/vmware-tanzu/vm-operator/test/e2e/utils"
	"github.com/vmware-tanzu/vm-operator/test/e2e/vmservice/common"
	"github.com/vmware-tanzu/vm-operator/test/e2e/vmservice/consts"
	"github.com/vmware-tanzu/vm-operator/test/e2e/vmservice/skipper"
	"github.com/vmware-tanzu/vm-operator/test/e2e/vmservice/vmservice"
)

// VIAdminSlowCLSpec verifies image readiness after a real, delayed OVF download
// without allowing a replacement download session to hide a polling failure.
func VIAdminSlowCLSpec(inputGetter func() VIAdminCLSpecInput) {
	It("keeps one download session alive during slow preparation with a five-second seed",
		Serial, Label("extended-functional", "content-library-backoff", "experimental"),
		SpecTimeout(50*time.Minute), func(ctx SpecContext) {
			input := inputGetter()
			skipper.SkipUnlessInfraIs(input.Config.InfraConfig.InfraName, consts.WCP)
			config := input.Config
			root := config.Variables["SlowContentLibraryDir"]
			publicURL := config.Variables["SlowContentLibraryURL"]
			if root == "" || publicURL == "" {
				Skip("set E2E_SLOW_CONTENT_LIBRARY_DIR and E2E_SLOW_CONTENT_LIBRARY_URL; see test/e2e/README.md")
			}
			Expect(filepath.Join(root, "lib.json")).To(BeAnExistingFile())
			u, err := url.Parse(publicURL)
			Expect(err).NotTo(HaveOccurred())
			Expect(u.Scheme).To(Equal("http"), "the test fixture serves plain HTTP on a testbed-reachable address")
			Expect(u.Host).NotTo(BeEmpty())
			Expect(u.Path).To(Equal("/lib.json"))
			Expect(u.RawQuery).To(BeEmpty())

			proxy := input.ClusterProxy.(*common.VMServiceClusterProxy)
			client := proxy.GetClient()
			vmopNS := config.GetVariable("VMOPNamespace")
			deploymentName := config.GetVariable("VMOPDeploymentName")
			envs, err := utils.GetCommandEnvVars(ctx, client, vmopNS, deploymentName, config.GetVariable("VMOPManagerCommand"))
			Expect(err).NotTo(HaveOccurred())
			seed, err := time.ParseDuration(envs["CONTENT_API_WAIT_SECS"])
			Expect(err).NotTo(HaveOccurred(), "configure CONTENT_API_WAIT_SECS=5s on the test operator before running")
			Expect(seed).To(Equal(5 * time.Second))

			By("serving a dedicated static content library from the test runner")
			fixture := clfixture.NewFixture(http.Dir(root))
			listener, err := net.Listen("tcp", config.GetVariable("SlowContentLibraryListenAddress"))
			Expect(err).NotTo(HaveOccurred())
			server := &http.Server{Handler: fixture, ReadHeaderTimeout: 10 * time.Second}
			DeferCleanup(func() {
				fixture.Release()
				Expect(server.Close()).To(Succeed())
			})
			go func() { _ = server.Serve(listener) }()

			By("creating a fresh on-demand subscription and evicting any cached OVF")
			wcpClient := input.WCPClient
			backingCL := vmservice.GetContentLibraryUUIDByName(consts.VMServiceCLName, wcpClient)
			backing, err := wcpClient.GetContentLibrary(backingCL)
			Expect(err).NotTo(HaveOccurred())
			Expect(backing.StorageBackings).NotTo(BeEmpty())
			suffix := capiutil.RandomString(6)
			clID, err := wcpClient.CreateSubscribedContentLibrary("e2e-slow-cl-"+suffix, publicURL, "", true,
				wcp.StorageBackingInfo{StorageBackings: backing.StorageBackings[:1]})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { Expect(wcpClient.DeleteSubscribedContentLibrary(clID)).To(Succeed()) })
			Expect(wcpClient.SyncSubscribedContentLibrary(clID)).To(Succeed())
			var itemID string
			Eventually(func(g Gomega) {
				ids, err := wcpClient.ListContentLibraryItems(clID)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(ids).To(HaveLen(1), "use a dedicated fixture containing exactly one OVF item")
				itemID = ids[0]
			}, 3*time.Minute, 5*time.Second).Should(Succeed())
			item, err := wcpClient.GetContentLibraryItem(itemID)
			Expect(err).NotTo(HaveOccurred())
			Expect(item.Type).To(Equal(wcp.OVF))
			vimClient := vcenter.NewVimClientFromKubeconfig(ctx, proxy.GetKubeconfigPath())
			DeferCleanup(vcenter.LogoutVimClient, vimClient)
			restClient, err := vcenter.NewRestClient(ctx, vimClient, testbed.AdminUsername, testbed.AdminPassword)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { Expect(restClient.Logout(context.Background())).To(Succeed()) })
			manager := library.NewManager(restClient)
			Expect(manager.EvictSubscribedLibraryItem(ctx, &library.Item{ID: itemID})).To(Succeed())

			By("creating an isolated namespace and watching the operator's session logs")
			nsContext, err := proxy.CreateWCPNamespace(ctx, config, wcp.NewVMServiceSpecDetails([]string{}, []string{}),
				config.InfraConfig.ManagementClusterConfig.Resources.StorageClassName, "slow-cl-"+suffix, input.ArtifactFolder)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { proxy.DeleteWCPNamespace(nsContext) })
			namespace := nsContext.GetNamespace().Name
			var deployment appsv1.Deployment
			Expect(client.Get(ctx, ctrlclient.ObjectKey{Namespace: vmopNS, Name: deploymentName}, &deployment)).To(Succeed())
			Expect(deployment.Status.ObservedGeneration).To(Equal(deployment.Generation), "wait for the operator rollout before running")
			Expect(deployment.Status.Replicas).To(BeNumerically(">", 0))
			Expect(deployment.Status.UpdatedReplicas).To(Equal(deployment.Status.Replicas))
			Expect(deployment.Status.ReadyReplicas).To(Equal(deployment.Status.Replicas))
			selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
			Expect(err).NotTo(HaveOccurred())
			var pods corev1.PodList
			Expect(client.List(ctx, &pods, ctrlclient.InNamespace(vmopNS), ctrlclient.MatchingLabelsSelector{Selector: selector})).To(Succeed())
			Expect(pods.Items).NotTo(BeEmpty())
			clientset, err := kubernetes.NewForConfig(proxy.GetRESTConfig())
			Expect(err).NotTo(HaveOccurred())
			logs := clfixture.NewSessionLogs(itemID)
			logCtx, stopLogs := context.WithCancel(ctx)
			DeferCleanup(stopLogs)
			since := metav1.Now()
			streams := 0
			for _, pod := range pods.Items {
				for _, container := range pod.Spec.Containers {
					if len(container.Command) == 0 || container.Command[0] != config.GetVariable("VMOPManagerCommand") {
						continue
					}
					stream, err := clientset.CoreV1().Pods(vmopNS).GetLogs(pod.Name, &corev1.PodLogOptions{
						Container: container.Name, Follow: true, SinceTime: &since,
					}).Stream(logCtx)
					Expect(err).NotTo(HaveOccurred())
					DeferCleanup(func() { _ = stream.Close() })
					streams++
					go logs.Read(logCtx, stream)
				}
			}
			Expect(streams).To(BeNumerically(">", 0), "no operator manager log streams were found")

			fixture.Hold()
			DeferCleanup(fixture.Release)
			Expect(wcpClient.AssociateImageRegistryContentLibrariesToNamespace(namespace,
				wcp.ContentLibrarySpec{ContentLibrary: clID})).To(Succeed())
			Eventually(func(g Gomega) {
				g.Expect(fixture.Pending()).To(BeNumerically(">", 0), "the OVF must be requested through the fixture")
				g.Expect(logs.Check(false)).To(Succeed())
			}, 5*time.Minute, 5*time.Second).Should(Succeed(),
				"require V(4) operator logs and the contentlibrary download path; a cached or bypassed download is not coverage")

			By("holding preparation through growth and the first plateau interval")
			// With the configured 5s seed, growth plus the first plateau wait is
			// at most (127+120)*5*1.1 seconds. Hold for 25m to cross both.
			// Do not GET or keepAlive the download session from the test: either
			// could renew its lease and hide an operator-side expiry regression.
			Consistently(func(g Gomega) {
				g.Expect(logs.Check(false)).To(Succeed())
				g.Expect(logs.HasDownload()).To(BeFalse(), "the held OVF must not become prepared")
				var images vmopv1.VirtualMachineImageList
				g.Expect(client.List(ctx, &images, ctrlclient.InNamespace(namespace))).To(Succeed())
				for _, image := range images.Items {
					if image.Status.ProviderItemID == itemID {
						g.Expect(meta.IsStatusConditionTrue(image.Status.Conditions, vmopv1.ReadyConditionType)).To(BeFalse())
					}
				}
			}, 25*time.Minute, 10*time.Second).Should(Succeed())

			By("releasing the OVF and waiting for this item's image to become ready")
			fixture.Release()
			Eventually(func(g Gomega) {
				g.Expect(logs.Check(true)).To(Succeed())
				var images vmopv1.VirtualMachineImageList
				g.Expect(client.List(ctx, &images, ctrlclient.InNamespace(namespace))).To(Succeed())
				var image *vmopv1.VirtualMachineImage
				for i := range images.Items {
					if images.Items[i].Status.ProviderItemID == itemID {
						g.Expect(image).To(BeNil(), "expected exactly one image for the fresh library item")
						image = &images.Items[i]
					}
				}
				g.Expect(image).NotTo(BeNil())
				g.Expect(meta.IsStatusConditionTrue(image.Status.Conditions, vmopv1.ReadyConditionType)).To(BeTrue())
				g.Expect(image.Status.Disks).NotTo(BeEmpty())
				g.Expect(image.Status.ProviderContentVersion).NotTo(BeEmpty())
			}, 15*time.Minute, 10*time.Second).Should(Succeed())
		})
}
