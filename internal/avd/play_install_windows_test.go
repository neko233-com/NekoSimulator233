package avd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func fakeSDKManager(t *testing.T) (Manager, string, string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "SDK with spaces")
	tools := filepath.Join(root, "cmdline-tools", "latest")
	jar := filepath.Join(tools, "lib", "sdkmanager-classpath.jar")
	if err := os.MkdirAll(filepath.Dir(jar), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jar, []byte("fake jar"), 0600); err != nil {
		t.Fatal(err)
	}
	jdk := filepath.Join(t.TempDir(), "JDK with spaces")
	java := filepath.Join(jdk, "bin", "java.exe")
	if err := os.MkdirAll(filepath.Dir(java), 0700); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Link(self, java); err != nil {
		t.Skipf("cannot link fake Java executable: %v", err)
	}
	record := filepath.Join(t.TempDir(), "args.json")
	image := filepath.Join(root, "system-images", "android-34", "google_apis_playstore", "x86_64", "system.img")
	t.Setenv("JAVA_HOME", jdk)
	t.Setenv("NEKO_TEST_FAKE_SDKMANAGER_RECORD", record)
	t.Setenv("NEKO_TEST_FAKE_SDKMANAGER_IMAGE", image)
	return Manager{SDK: root}, record, tools, jar
}

func TestPlayImageInstallTargetsOnlyRequestedPackage(t *testing.T) {
	m, record, tools, jar := fakeSDKManager(t)
	var progress []int
	result, err := m.InstallPlayImage(context.Background(), 34, func(n int) { progress = append(progress, n) })
	if err != nil || result.API != 34 || result.AlreadyInstalled {
		t.Fatalf("install: %+v %v", result, err)
	}
	b, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	if err := json.Unmarshal(b, &args); err != nil {
		t.Fatal(err)
	}
	want := []string{"-Dcom.android.sdklib.toolsdir=" + tools, "-classpath", jar, "com.android.sdklib.tool.sdkmanager.SdkManagerCli", "--sdk_root=" + m.SDK, "--install", "system-images;android-34;google_apis_playstore;x86_64", "--channel=0"}
	if !slices.Equal(args, want) {
		t.Fatalf("arguments %q, want %q", args, want)
	}
	if len(progress) == 0 || progress[len(progress)-1] != 100 {
		t.Fatalf("missing progress: %v", progress)
	}
	t.Setenv("NEKO_TEST_FAKE_SDKMANAGER_MODE", "license")
	result, err = m.InstallPlayImage(context.Background(), 34, nil)
	if err != nil || !result.AlreadyInstalled {
		t.Fatalf("existing image was not preserved: %+v %v", result, err)
	}
	_, imageDir, err := playImagePath(m.SDK, 34)
	if err != nil {
		t.Fatal(err)
	}
	props := filepath.Join(imageDir, "source.properties")
	wrong := []byte("Pkg.Revision=1\nAndroidVersion.ApiLevel=34\nSystemImage.Abi=x86_64\nSystemImage.TagId=google_apis\n")
	if err := os.WriteFile(props, wrong, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.InstallPlayImage(context.Background(), 34, nil); err == nil {
		t.Fatal("wrong image identity accepted")
	}
	if after, err := os.ReadFile(props); err != nil || string(after) != string(wrong) {
		t.Fatal("mismatched image was modified")
	}
}

func TestPlayImageInstallPreservesPartialDirectory(t *testing.T) {
	m, record, _, _ := fakeSDKManager(t)
	_, dir, err := playImagePath(m.SDK, 34)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(keep, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.InstallPlayImage(context.Background(), 34, nil); err == nil {
		t.Fatal("accepted incomplete existing directory")
	}
	if b, err := os.ReadFile(keep); err != nil || string(b) != "preserve" {
		t.Fatal("partial directory changed")
	}
	if _, err := os.Stat(record); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("sdkmanager was unexpectedly invoked")
	}
	if _, err := m.InstallPlayImage(context.Background(), 33, nil); err == nil {
		t.Fatal("unsupported API accepted")
	}
}

func TestPlayImageInstallRejectsLinkedParent(t *testing.T) {
	m, record, _, _ := fakeSDKManager(t)
	parent := filepath.Join(m.SDK, "system-images")
	if err := os.MkdirAll(parent, 0700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	link := filepath.Join(parent, "android-34")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("directory symlink unavailable: %v", err)
	}
	defer os.Remove(link)
	if _, err := m.InstallPlayImage(context.Background(), 34, nil); err == nil {
		t.Fatal("linked image parent accepted")
	}
	if _, err := os.Stat(record); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("sdkmanager ran through linked parent")
	}
}

func TestPlayImageInstallRejectsEmptyImage(t *testing.T) {
	m, record, _, _ := fakeSDKManager(t)
	_, dir, err := playImagePath(m.SDK, 34)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "system.img"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.InstallPlayImage(context.Background(), 34, nil); err == nil {
		t.Fatal("empty image treated as installed")
	}
	if _, err := os.Stat(record); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("empty image was overwritten")
	}
}

func TestPlayImageInstallHandlesLicenseAndCancellation(t *testing.T) {
	m, _, _, _ := fakeSDKManager(t)
	t.Setenv("NEKO_TEST_FAKE_SDKMANAGER_MODE", "license")
	if _, err := m.InstallPlayImage(context.Background(), 34, nil); err == nil || !strings.Contains(err.Error(), "许可证") {
		t.Fatalf("license failure: %v", err)
	}
	t.Setenv("NEKO_TEST_FAKE_SDKMANAGER_MODE", "wait")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := m.InstallPlayImage(ctx, 34, func(n int) {
		if n >= 10 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestPlayImageInstallLockRespectsCancellation(t *testing.T) {
	root := t.TempDir()
	unlock, err := lockSDKInstall(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if release, err := lockSDKInstall(ctx, root); !errors.Is(err, context.DeadlineExceeded) {
		if release != nil {
			release()
		}
		t.Fatalf("second installation did not wait: %v", err)
	}
}

func TestPlayImageRequiresNativeJavaExecutable(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if !nativeJava(self) {
		t.Fatal("native test executable was rejected")
	}
	path := filepath.Join(t.TempDir(), "java.exe")
	if err := os.WriteFile(path, []byte("not an executable"), 0600); err != nil {
		t.Fatal(err)
	}
	if nativeJava(path) {
		t.Fatal("invalid executable accepted")
	}
}

func TestPlayImageFindsAndroidStudioJBR(t *testing.T) {
	studio := t.TempDir()
	java := filepath.Join(studio, "jbr", "bin", "java.exe")
	if err := os.MkdirAll(filepath.Dir(java), 0700); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Link(self, java); err != nil {
		t.Skipf("cannot link fake JBR: %v", err)
	}
	t.Setenv("JAVA_HOME", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("JDK_HOME", "")
	t.Setenv("ANDROID_STUDIO_HOME", studio)
	got, err := sdkManagerJava()
	if err != nil || got != java {
		t.Fatalf("JBR discovery: %q %v", got, err)
	}
}

func TestSDKProgressIsBoundedAndMonotonic(t *testing.T) {
	var got []int
	p := &sdkProgress{last: -1, notify: func(n int) { got = append(got, n) }}
	for _, chunk := range []string{"download 1", "0%", " 10%", " 5%", " 100%", " 999%", strings.Repeat("x", 5000), "license ", "not accepted"} {
		if _, err := p.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(got, []int{10, 100}) || !p.licenseIssue || len(p.tail) > 4096 {
		t.Fatalf("progress=%v license=%v tail=%d", got, p.licenseIssue, len(p.tail))
	}
}
