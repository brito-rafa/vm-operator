// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package contentlibrary_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestContentLibrary(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Content Library E2E Fixture Suite")
}
