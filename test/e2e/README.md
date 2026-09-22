# VM Operator E2E Test Framework

This directory contains the End-to-End (E2E) test framework for VM Operator, providing comprehensive testing capabilities.

## Overview

The E2E framework validates VM Operator functionality by:
- Creating and managing VirtualMachine resources in real Kubernetes clusters
- Interacting with actual vSphere infrastructure 
- Testing feature functionality across different networking topologies (VDS/NSX)
- Validating integration with WCP components and services

## Architecture

```
test/e2e/
├── vmservice/              # Main test suite for VM Service functionality
│   ├── config/            # Test configuration files (wcp.yaml)
│   ├── vmservice/         # Core VM lifecycle and feature tests
│   │   └── virtualmachine/ # VM-specific test scenarios
│   ├── vmserviceapp/      # Application deployment tests
│   ├── lib/               # Test libraries and helpers
│   ├── common/            # Shared test utilities
│   └── consts/            # Test constants and labels
├── framework/             # Core testing framework components
├── infrastructure/        # Infrastructure interaction (vSphere, WCP)
│   └── vsphere/          # vSphere-specific clients and utilities
├── manifestbuilders/      # Test manifest generation utilities
├── utils/                 # General test utilities
├── fixtures/              # Test data and YAML fixtures
└── testutils/            # Additional testing utilities
```

## Quick Start

### Prerequisites

1. **Access to a WCP cluster** with VM Operator deployed
2. **vSphere environment** with proper permissions
3. **Kubeconfig** for the WCP supervisor cluster
4. **Go 1.26+** installed locally

### Configuration

1. **Set up the env** for your WCP cluster:
The following will export all the required environment variables
required for the test and download the kubeconfig from the Supervisor cluster.
The kubeconfig will be stored under a unique `~/.kube/<wcpIp>.kubeconfig.<random>`
file (exported as both `KUBECONFIG` and, with `--e2e`, `E2E_KUBECONFIG_PATH`,
which the E2E test reads from). The unique filename means concurrent local
runs (e.g. against two testbeds, or two runs against the same testbed in
separate terminals) won't stomp on each other's kubeconfig.

```bash
source ./hack/e2e/setup-testbed-env.sh <testbedInfo.json> --e2e
```

2. **Running Tests**

#### All E2E Tests
```bash
make test-e2e
```

Use `make test-e2e` (auto-detects), `make test-e2e-prebuilt`, or `make test-e2e-ginkgo`. The first uses `./e2e-tests` at the repo root when present (e.g. slim E2E image). Override the binary path with `E2E_PREBUILT_BINARY=/path/to/binary`.

#### Test Categories

**Smoke Tests** (quick validation):
```bash
make e2e-smoke
```

**Core Functional Tests** (comprehensive functionality):
```bash
make e2e-core
```

**Extended Functional Tests** (advanced features):
```bash
make e2e-extended
```

#### Focused Test Execution

**Run specific test patterns**:
```bash
make test-e2e TEST_FOCUS="VM-HARDWARE"
```

Precompiled `./e2e-tests`: pass `-e2e.*` and `--ginkgo.*` in one contiguous argument list (as `make test-e2e-prebuilt` does). Do **not** put `--` between them; otherwise `--ginkgo.focus`, `--ginkgo.label-filter`, etc. are ignored and nearly all specs still run.

**Skip specific tests**:
```bash
make test-e2e TEST_SKIP="encryption|snapshot"
```

**Label-based filtering**:
```bash
make test-e2e LABEL_FILTER="networking && !encryption"
```

**Flake retry**:
```bash
make test-e2e FLAKE_ATTEMPTS=3
```

**Use specific namespace**:
```bash
E2E_NAMESPACE=my-test-ns make e2e-smoke
```

**Combine multiple options**:
```bash
E2E_NAMESPACE=debug-ns TEST_FOCUS="VM-HARDWARE" FLAKE_ATTEMPTS=2 make e2e-core
```

## Test Categories and Labels

Tests are organized using Ginkgo labels for easy filtering:

### Core Labels
- `smoke` - Quick validation tests
- `core-functional` - Core feature tests
- `extended-functional` - Advanced feature tests
- `experimental` - Excluded from all CI targets; requires infrastructure unavailable in the standard `vds_standard_medium` testbed (e.g. encryption-capable storage)

## Configuration

### Main Configuration File

The primary configuration is in `vmservice/config/wcp.yaml`:

### Environment Variables

| Variable | Description | Default | Makefile Support |
|----------|-------------|---------|------------------|
| `NETWORK` | Networking topology (vds/nsx) | `vds` | ❌ |
| `STORAGE_CLASS` | Storage class for VMs | `wcpglobal-storage-profile` | ❌ |
| `E2E_KUBECONFIG_PATH` | Supervisor kubeconfig path (set by `setup-testbed-env.sh --e2e`; a unique file per invocation avoids concurrent local runs stomping on each other) | `~/.kube/wcp-config` | ❌ |
| `E2E_NAMESPACE` | Fixed namespace for tests | Random namespace | ✅ |
| `E2E_SLOW_CONTENT_LIBRARY_DIR` | Static single-item published library root for the experimental slow-preparation test | Unset; test skipped | ❌ |
| `E2E_SLOW_CONTENT_LIBRARY_URL` | vCenter-reachable HTTP URL for that fixture's `/lib.json` | Unset; test skipped | ❌ |
| `E2E_SLOW_CONTENT_LIBRARY_LISTEN_ADDRESS` | Test runner address on which to serve the slow library fixture | `0.0.0.0:8080` | ❌ |
| `TEST_FOCUS` | Ginkgo focus pattern | All tests | ✅ |
| `TEST_SKIP` | Ginkgo skip pattern | No skips | ✅ |
| `LABEL_FILTER` | Ginkgo label filter | No filter | ✅ |
| `FLAKE_ATTEMPTS` | Retry count for flaky tests | No retries | ✅ |

#### Namespace Management

The `E2E_NAMESPACE` variable controls which Kubernetes namespace the tests use. It's automatically passed through all Makefile targets to the test configuration.

**Default behavior** (random namespace):
```bash
make e2e-smoke  # Creates random namespace like "vmservice-e2e-abc123"
```

**Fixed namespace** (reuse existing namespace):
```bash
make e2e-smoke E2E_NAMESPACE=my-test-ns  # Uses "my-test-ns" namespace
```

**Benefits of fixed namespace**:
- Faster test execution (no namespace creation/deletion)
- Easier debugging 
- Shared test environment setup
- Consistent test isolation

## Writing New Tests

### Slow content-library preparation

`VI-ADMIN-CL-SLOW-PREPARATION` is labeled `content-library-backoff`, `extended-functional`, and `experimental`. It uses the deployed operator and a real on-demand vCenter subscription. The test serves a dedicated static library, evicts the subscribed item's cached files, holds OVF GETs for 25 minutes, and then releases them. It requires the resulting namespace-scoped `VirtualMachineImage` to become Ready with disks and a provider content version. Operator logs must show that the original download session performed both preparation and completion; a retry with a new session fails the test. The test does not query or renew that session, because doing so could conceal an idle-expiry bug.

Run on a dedicated testbed with `CONTENT_API_WAIT_SECS=5s` and verbosity `--v=4` or higher configured on the operator before the run. Wait for the deployment rollout to finish. The test reads these settings and logs; it does not alter the deployment. The deployed feature configuration must exercise `pkg/providers/vsphere/contentlibrary`'s download-session path. A cache or alternate image path that bypasses it fails the preparation assertion instead of producing a false pass. Keep the normal content-library idle timeout so the test can detect session expiry.

Prepare a **dedicated static published library export** containing exactly one valid, security-compliant OVF image with at least one disk. Put `lib.json` at its root, alongside all referenced item manifests and content files. All manifest download links must be relative and resolve within this directory; links to the original publisher would bypass the fixture. The directory is served over HTTP for the duration of the test. The test runner's listening port must be reachable from vCenter, including through any container port mapping or firewall. Metadata and HEAD requests are served immediately. After the initial subscription synchronization, OVF GETs trickle one byte every ten seconds and withhold completion until released, preserving the original content and checksum while avoiding an idle HTTP connection. Use an OVF descriptor larger than 256 bytes so that the slow prefix lasts through the hold. If the target vCenter retains the OVF after eviction or prefetches it before VM Operator prepares it, the test fails its fixture/path assertion and requires a testbed configuration that actually exercises slow preparation.

```bash
export E2E_SLOW_CONTENT_LIBRARY_DIR=/path/to/dedicated-library-export
export E2E_SLOW_CONTENT_LIBRARY_URL=http://TEST_RUNNER_IP:8080/lib.json
export E2E_SLOW_CONTENT_LIBRARY_LISTEN_ADDRESS=0.0.0.0:8080
make test-e2e-ginkgo \
  TEST_FOCUS=VI-ADMIN-CL-SLOW-PREPARATION \
  LABEL_FILTER='content-library-backoff && experimental'
```

Allow up to 50 minutes plus normal suite setup and cleanup. The 25-minute hold crosses the growth phase and first plateau wait at a five-second seed, including jitter; completion has a separate 15-minute deadline. The test is serial and always releases held requests and removes its namespace and subscribed library, even after failure. Operator restarts or interrupted log streams fail the session-continuity check. This is regression coverage for the session-lifetime issue and can fail until that product fix is applied. Keep `experimental` until the scenario passes on a real WCP cluster.

### Test Structure

Follow the established patterns in `vmservice/vmservice/virtualmachine/`:

```go
var _ = Describe("My Feature", Label("my-feature"), func() {
    var (
        ctx          context.Context
        vm           *vmopv1.VirtualMachine
        vmKey        types.NamespacedName
    )

    BeforeEach(func() {
        // Setup test environment
        vm = &vmopv1.VirtualMachine{
            ObjectMeta: metav1.ObjectMeta{
                Name:      "test-vm",
                Namespace: namespace,
            },
            Spec: vmopv1.VirtualMachineSpec{
                // VM specification
            },
        }
    })

    When("feature is enabled", func() {
        It("should work correctly", func() {
            // Test implementation
            Expect(client.Create(ctx, vm)).To(Succeed())
            
            Eventually(func(g Gomega) {
                g.Expect(client.Get(ctx, vmKey, vm)).To(Succeed())
                g.Expect(vm.Status.Phase).To(Equal(vmopv1.Created))
            }).Should(Succeed())
        })
    })
})
```

### Best Practices

1. **Use descriptive test names** that explain the scenario
2. **Apply appropriate labels** for filtering
3. **Use Eventually/Consistently** for async operations
4. **Clean up resources** in AfterEach hooks
5. **Test both positive and negative cases**
6. **Use manifest builders** for complex objects
7. **Validate status conditions** and events

### Helper Libraries

#### VM Operations (`lib/vmoperator`)
```go
vmoperator.WaitForVirtualMachineCreation(ctx, config, client, vm)
vmoperator.VerifyVirtualMachinePowerState(ctx, config, client, vm, vmopv1.VirtualMachinePowerStateOn)
```

#### Manifest Builders (`manifestbuilders`)
```go
vm := manifestbuilders.CreateVMManifest(manifestbuilders.CreateVMParams{
    VMName:    "test-vm",
    Namespace: namespace,
    VMClass:   "best-effort-small",
    VMImage:   "photon-5.0",
})
```

#### Infrastructure Access (`infrastructure/vsphere`)
```go
vcClient := vcenter.NewVimClientFromKubeconfig(ctx, kubeconfigPath)
```

### Debug Mode

**Enable verbose output**:
```bash
make test-e2e GINKGO_ARGS="-v --trace"
```

**Test-specific debugging**:
```go
By("Debug checkpoint: VM creation")
framework.Logf("VM status: %+v", vm.Status)
```

## Development Workflow

1. **Write tests** following established patterns
2. **Add appropriate labels** for categorization
3. **Test locally** with `make test-e2e TEST_FOCUS="MyFeature"`
4. **Clean up artifacts** with `make clean` before Docker builds
5. **Update documentation** for new test categories

### Cleanup and Optimization

**Remove build artifacts** before Docker operations:
```bash
make clean  # Removes *.test binaries, coverage files, and build artifacts
```

## Integration Points

- **VM Operator Controllers** - Tests controller reconciliation
- **vSphere APIs** - Validates infrastructure integration  
- **WCP Services** - Tests platform service interactions
- **Kubernetes APIs** - Validates CRD and webhook behavior
- **Feature Gates** - Tests feature flag functionality

This E2E framework ensures VM Operator reliability across diverse environments and use cases, providing confidence in production deployments.
