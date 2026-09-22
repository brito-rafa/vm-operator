// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package contentlibrary_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing/fstest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"
	"github.com/vmware-tanzu/vm-operator/test/e2e/infrastructure/vsphere/contentlibrary"
)

var _ = Describe("Slow content library fixture", Label(testlabels.API), func() {
	var fixture *contentlibrary.Fixture
	BeforeEach(func() {
		fixture = contentlibrary.NewFixture(http.FS(fstest.MapFS{
			"lib.json": {Data: []byte(`{"items":[]}`)},
			"item.ovf": {Data: []byte("<Envelope/>")},
		}))
		fixture.Hold()
		DeferCleanup(fixture.Release)
	})

	It("serves metadata and HEAD requests while holding an OVF GET", func() {
		for _, request := range []*http.Request{
			httptest.NewRequest(http.MethodGet, "/lib.json", nil),
			httptest.NewRequest(http.MethodHead, "/item.ovf", nil),
		} {
			response := httptest.NewRecorder()
			fixture.ServeHTTP(response, request)
			Expect(response.Code).To(Equal(http.StatusOK))
		}
		response := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			defer close(done)
			fixture.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/item.ovf", nil))
		}()
		Eventually(fixture.Pending).Should(Equal(1))
		Consistently(done, 20*time.Millisecond).ShouldNot(BeClosed())
		fixture.Release()
		Eventually(done).Should(BeClosed())
		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(response.Body.String()).To(Equal("<Envelope/>"))
		Expect(fixture.Pending()).To(BeZero())
	})

	It("lets cancelled downloads exit without waiting for release", func() {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		response := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			defer close(done)
			fixture.ServeHTTP(response, httptest.NewRequestWithContext(ctx, http.MethodGet, "/item.ovf", nil))
		}()
		Eventually(fixture.Pending).Should(Equal(1))
		cancel()
		Eventually(done).Should(BeClosed())
		Expect(fixture.Pending()).To(BeZero())
		Expect(response.Body.String()).NotTo(Equal("<Envelope/>"))
	})
})

var _ = Describe("Download session log observation", Label(testlabels.API), func() {
	DescribeTable("recognizes a completed session in buffered logs at shutdown", func(lines string) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		logs := contentlibrary.NewSessionLogs("item-1")
		logs.Read(ctx, strings.NewReader(lines))
		Expect(logs.Check(true)).To(Succeed())
		Expect(logs.HasDownload()).To(BeTrue())
	},
		Entry("klog text", `"download session for item created" itemID="item-1" sessionID="session-1"
"request posted to prepare file" itemID="item-1" sessionID="session-1"
"Downloaded file" itemID="item-1" sessionID="session-1"`),
		Entry("JSON", `{"msg":"download session for item created","itemID":"item-1","sessionID":"session-1"}
{"msg":"request posted to prepare file","itemID":"item-1","sessionID":"session-1"}
{"msg":"Downloaded file","itemID":"item-1","sessionID":"session-1"}`),
	)

	It("does not let a replacement session hide a failed attempt", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		logs := contentlibrary.NewSessionLogs("item-1")
		logs.Read(ctx, strings.NewReader(`"download session for item created" itemID="item-1" sessionID="first"
"request posted to prepare file" itemID="item-1" sessionID="first"
"download session for item created" itemID="item-1" sessionID="retry"
"request posted to prepare file" itemID="item-1" sessionID="retry"
"Downloaded file" itemID="item-1" sessionID="retry"`))
		Expect(logs.Check(true)).NotTo(Succeed())
	})

	It("ignores sessions belonging to other items", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		logs := contentlibrary.NewSessionLogs("item-1")
		logs.Read(ctx, strings.NewReader(`"download session for item created" itemID="item-10" sessionID="other"
"download session for item created" itemID="item-1" sessionID="session-1"
"request posted to prepare file" itemID="item-1" sessionID="session-1"`))
		Expect(logs.Check(false)).To(Succeed())
		Expect(logs.Check(true)).NotTo(Succeed())
		Expect(logs.HasDownload()).To(BeFalse())
	})

	It("rejects an interrupted stream even if it contained a successful download", func() {
		logs := contentlibrary.NewSessionLogs("item-1")
		logs.Read(context.Background(), strings.NewReader(`"download session for item created" itemID="item-1" sessionID="session-1"
"request posted to prepare file" itemID="item-1" sessionID="session-1"
"Downloaded file" itemID="item-1" sessionID="session-1"`))
		Expect(logs.Check(true)).To(MatchError(ContainSubstring("log stream ended")))
	})
})
