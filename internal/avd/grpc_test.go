package avd

import (
	"bufio"
	"bytes"
	"context"
	"image/png"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neko233-com/NekoSimulator233/internal/display"
	"github.com/neko233-com/NekoSimulator233/internal/testutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/mem"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
)

type touchServerInterface interface{ touchServerMarker() }
type touchServer struct {
	mu    sync.Mutex
	calls []rawProto
}

func (*touchServer) touchServerMarker() {}
func (s *touchServer) handle(_ context.Context, p *rawProto) (*rawProto, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, append(rawProto(nil), (*p)...))
	if len(s.calls) == 1 {
		return nil, status.Error(codes.Unavailable, "simulated lost response")
	}
	result := rawProto(nil)
	return &result, nil
}

func TestTouchFailureStillReleasesAllPossibleContacts(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer(grpc.ForceServerCodecV2(protoCodec{}))
	service := &touchServer{}
	server.RegisterService(&grpc.ServiceDesc{ServiceName: "android.emulation.control.EmulatorController", HandlerType: (*touchServerInterface)(nil), Methods: []grpc.MethodDesc{{MethodName: "sendTouch", Handler: func(srv any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
		var req rawProto
		if err := dec(&req); err != nil {
			return nil, err
		}
		return srv.(*touchServer).handle(ctx, &req)
	}}}}, service)
	go server.Serve(listener)
	defer server.Stop()
	c, err := DialController(listener.Addr().(*net.TCPAddr).Port, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	c.width, c.height = 720, 1280
	if err = c.SetTouches([]TouchPoint{{ID: 0, X: 100, Y: 100}, {ID: 1, X: 200, Y: 200}}); err == nil {
		t.Fatal("expected simulated transport failure")
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if len(service.calls) != 2 {
		t.Fatalf("expected release attempt, got %d requests", len(service.calls))
	}
	data := []byte(service.calls[1])
	count := 0
	for len(data) != 0 {
		n, typ, used := protowire.ConsumeTag(data)
		if used < 0 || n != 1 || typ != protowire.BytesType {
			t.Fatal("invalid touch event")
		}
		data = data[used:]
		contact, used := protowire.ConsumeBytes(data)
		if used < 0 {
			t.Fatal("invalid contact")
		}
		data = data[used:]
		pressure := uint64(1)
		for len(contact) != 0 {
			field, typ, used := protowire.ConsumeTag(contact)
			if used < 0 || typ != protowire.VarintType {
				t.Fatal("invalid contact field")
			}
			contact = contact[used:]
			value, used := protowire.ConsumeVarint(contact)
			if used < 0 {
				t.Fatal("invalid contact value")
			}
			contact = contact[used:]
			if field == 4 {
				pressure = value
			}
		}
		if pressure != 0 {
			t.Fatal("expected release pressure")
		}
		count++
	}
	if count != 2 {
		t.Fatalf("released %d contacts, want 2", count)
	}
}

func TestTouchValidation(t *testing.T) {
	c := &Controller{width: 720, height: 1280, contacts: make(map[int]TouchPoint)}
	for _, points := range [][]TouchPoint{
		{{ID: -1, X: 0, Y: 0}}, {{ID: 10, X: 0, Y: 0}}, {{ID: 0, X: 720, Y: 0}},
		{{ID: 1, X: 1, Y: 1}, {ID: 1, X: 2, Y: 2}}, make([]TouchPoint, 11),
	} {
		if err := c.SetTouches(points); err == nil {
			t.Fatalf("accepted invalid contacts: %+v", points)
		}
	}
	if len(c.contacts) != 0 {
		t.Fatal("invalid contacts changed held state")
	}
}

func TestTouchLockHelper(t *testing.T) {
	path := os.Getenv("NEKO_TEST_TOUCH_LOCK")
	if path == "" {
		t.Skip("subprocess helper")
	}
	c := &Controller{touchLockPath: path}
	err := c.lockTouch()
	if os.Getenv("NEKO_TEST_TOUCH_BUSY") == "1" {
		if err == nil {
			c.unlockTouch()
			t.Fatal("second process acquired an active gesture")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	c.unlockTouch()
}

func TestTouchLeaseCoordinatesProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gesture.lock")
	c := &Controller{touchLockPath: path}
	if err := c.lockTouch(); err != nil {
		t.Fatal(err)
	}
	defer c.unlockTouch()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	check := func(busy string) {
		t.Helper()
		cmd := exec.Command(self, "-test.run=^TestTouchLockHelper$")
		cmd.Env = append(os.Environ(), "NEKO_TEST_TOUCH_LOCK="+path, "NEKO_TEST_TOUCH_BUSY="+busy)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("touch lease subprocess: %v %s", err, output)
		}
	}
	check("1")
	c.unlockTouch()
	check("0")
}

func TestParseImage(t *testing.T) {
	format := appendInt(nil, 1, 1)
	format = appendInt(format, 3, 2)
	format = appendInt(format, 4, 1)
	msg := appendBytes(nil, 1, format)
	msg = appendBytes(msg, 4, []byte{1, 2, 3, 255, 4, 5, 6, 255})
	w, h, pixels, err := parseImage(msg)
	if err != nil || w != 2 || h != 1 || len(pixels) != 8 {
		t.Fatalf("invalid frame: %d %d %d %v", w, h, len(pixels), err)
	}
	if _, _, _, err := parseImage(msg[:len(msg)-1]); err == nil {
		t.Fatal("truncated frame accepted")
	}
	var frame frameResponse
	parts := mem.BufferSlice{mem.SliceBuffer(msg[:3]), mem.SliceBuffer(msg[3:])}
	if err := (protoCodec{}).Unmarshal(parts, &frame); err != nil {
		t.Fatal(err)
	}
	if frame.frame.Bounds().Dx() != 2 || frame.frame.Pix[0] != 1 || frame.frame.Pix[4] != 4 {
		t.Fatal("fragmented frame was not decoded correctly")
	}
}

func TestLiveTouchEvents(t *testing.T) {
	c := liveController(t)
	if _, err := c.Capture(nil); err != nil {
		t.Fatal(err)
	}
	adb := filepath.Join(os.Getenv("LOCALAPPDATA"), "Android", "Sdk", "platform-tools", "adb.exe")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, adb, "-s", "emulator-5578", "shell", "getevent", "-lt")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan []byte, 1)
	go func() { data, _ := io.ReadAll(pipe); done <- data }()
	time.Sleep(300 * time.Millisecond)
	if err = c.TouchDown(100, 100); err != nil {
		cancel()
		cmd.Wait()
		t.Fatal(err)
	}
	if err = c.TouchUp(); err != nil {
		cancel()
		cmd.Wait()
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	cancel()
	data := <-done
	_ = cmd.Wait()
	if !bytes.Contains(data, []byte("ABS_MT_TRACKING_ID")) {
		t.Fatal("guest input device did not report touch tracking")
	}
	if !bytes.Contains(data, []byte("ffffffff")) {
		t.Fatal("guest did not report touch release")
	}
	t.Log("isolated Android guest reported a touch tracking event")
}

func TestLiveMultiTouchAndCloseRelease(t *testing.T) {
	c := liveController(t)
	if _, err := c.Capture(nil); err != nil {
		t.Fatal(err)
	}
	adb := filepath.Join(os.Getenv("LOCALAPPDATA"), "Android", "Sdk", "platform-tools", "adb.exe")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, adb, "-s", "emulator-5578", "shell", "getevent", "-lt")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan []byte, 1)
	go func() { data, _ := io.ReadAll(pipe); done <- data }()
	time.Sleep(300 * time.Millisecond)
	err = c.SetTouches([]TouchPoint{{ID: 0, X: 150, Y: 500}, {ID: 1, X: 500, Y: 500}})
	if err == nil {
		time.Sleep(150 * time.Millisecond)
		err = c.SetTouches([]TouchPoint{{ID: 0, X: 100, Y: 500}, {ID: 1, X: 550, Y: 500}})
	}
	if err == nil {
		time.Sleep(150 * time.Millisecond)
		err = c.SetTouches([]TouchPoint{{ID: 1, X: 550, Y: 500}})
	}
	if err == nil {
		time.Sleep(150 * time.Millisecond)
		err = c.Close()
	}
	time.Sleep(300 * time.Millisecond)
	cancel()
	data := <-done
	_ = cmd.Wait()
	if err != nil {
		t.Fatal(err)
	}
	active := make(map[uint64]bool)
	var slot uint64
	maxActive, releases := 0, 0
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		v, e := strconv.ParseUint(fields[len(fields)-1], 16, 32)
		if e != nil {
			continue
		}
		switch {
		case strings.Contains(line, "ABS_MT_SLOT"):
			slot = v
		case strings.Contains(line, "ABS_MT_TRACKING_ID"):
			if v == 0xffffffff {
				delete(active, slot)
				releases++
			} else {
				active[slot] = true
				if len(active) > maxActive {
					maxActive = len(active)
				}
			}
		}
	}
	if maxActive < 2 || releases < 2 || len(active) != 0 {
		t.Fatalf("guest multitouch: peak=%d releases=%d remaining=%d", maxActive, releases, len(active))
	}
	t.Logf("isolated guest observed %d simultaneous touches and both releases", maxActive)
}

func TestLiveEvdevKeyEvents(t *testing.T) {
	c := liveController(t)
	adb := filepath.Join(os.Getenv("LOCALAPPDATA"), "Android", "Sdk", "platform-tools", "adb.exe")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, adb, "-s", "emulator-5578", "shell", "getevent", "-lt")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan []byte, 1)
	go func() { data, _ := io.ReadAll(pipe); done <- data }()
	time.Sleep(300 * time.Millisecond)
	request := appendInt(nil, 1, 1)
	request = appendInt(request, 3, 30) // evdev KEY_A, down
	if _, err = c.call("sendKey", rawProto(request), 2*time.Second); err != nil {
		cancel()
		<-done
		_ = cmd.Wait()
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	request = appendInt(request, 2, 1) // up
	if _, err = c.call("sendKey", rawProto(request), 2*time.Second); err != nil {
		cancel()
		<-done
		_ = cmd.Wait()
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	cancel()
	data := <-done
	_ = cmd.Wait()
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "KEY_A") {
			t.Log(strings.TrimSpace(line))
		}
	}
	if !bytes.Contains(data, []byte("KEY_A")) {
		t.Fatal("Android did not report KEY_A")
	}
}

func TestLiveUSBKeyEvents(t *testing.T) {
	c := liveController(t)
	adb := filepath.Join(os.Getenv("LOCALAPPDATA"), "Android", "Sdk", "platform-tools", "adb.exe")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, adb, "-s", "emulator-5578", "shell", "getevent", "-lt")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan []byte, 1)
	go func() { data, _ := io.ReadAll(pipe); done <- data }()
	time.Sleep(300 * time.Millisecond)
	if err = c.KeyDown(0x04); err != nil {
		t.Fatal(err)
	}
	if err = c.KeyDown(0x04); err != nil {
		t.Fatal(err)
	} // host repeat must not add a second press
	time.Sleep(100 * time.Millisecond)
	if err = c.KeyUp(0x04); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if err = c.KeyDown(0x04); err != nil {
		t.Fatal(err)
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	} // connection close must release held keys
	time.Sleep(300 * time.Millisecond)
	cancel()
	data := <-done
	_ = cmd.Wait()
	var downs, ups int
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "KEY_A") {
			if strings.HasSuffix(strings.TrimSpace(line), "DOWN") {
				downs++
			}
			if strings.HasSuffix(strings.TrimSpace(line), "UP") {
				ups++
			}
		}
	}
	if downs != 2 || ups != 2 {
		t.Fatalf("expected two balanced key presses, got down=%d up=%d", downs, ups)
	}
}

func TestLiveNavigation(t *testing.T) {
	_ = liveController(t) // guards the isolated device and holds its test lock
	root := os.Getenv("NEKO_TEST_AGENT_AVD_HOME")
	if root == "" {
		t.Skip("set NEKO_TEST_AGENT_AVD_HOME")
	}
	m, err := DiscoverManager()
	if err != nil {
		t.Fatal(err)
	}
	m.Home = root
	m.Name = "NekoValidation"
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	instance, err := m.Ready(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c, err := instance.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	adb := filepath.Join(m.SDK, "platform-tools", "adb.exe")
	if err = exec.Command(adb, "-s", instance.Serial, "shell", "am", "start", "-a", "android.settings.SETTINGS").Run(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if err = c.Navigate(display.Home); err != nil {
		t.Fatal(err)
	}
	var foreground string
	for i := 0; i < 15; i++ {
		out, _ := exec.Command(adb, "-s", instance.Serial, "shell", "dumpsys", "activity", "activities").Output()
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "topResumedActivity") {
				foreground = line
				break
			}
		}
		if strings.Contains(foreground, "NexusLauncherActivity") {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("navigation did not return isolated guest to its launcher")
}

func liveController(t testing.TB) *Controller {
	t.Helper()
	path := os.Getenv("NEKO_TEST_AVD_INFO")
	if path == "" {
		t.Skip("set NEKO_TEST_AVD_INFO to the owned emulator discovery file")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	values := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		k, v, ok := strings.Cut(scanner.Text(), "=")
		if ok {
			values[k] = strings.TrimSpace(v)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if values["avd.name"] != "NekoValidation" || values["port.serial"] != "5578" {
		t.Fatal("refusing a non-validation instance")
	}
	testutil.LockAVD(t, filepath.Dir(values["avd.dir"]))
	port, err := strconv.Atoi(values["grpc.port"])
	if err != nil {
		t.Fatal(err)
	}
	c, err := DialController(port, values["grpc.token"])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func BenchmarkLiveCapture(b *testing.B) {
	c := liveController(b)
	frame, err := c.Capture(nil)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		frame, err = c.Capture(frame)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func TestLiveCapture(t *testing.T) {
	c := liveController(t)
	frame, err := c.Capture(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("independent emulator frame %dx%d", frame.Bounds().Dx(), frame.Bounds().Dy())
	if frame.Bounds().Dx() != 720 || frame.Bounds().Dy() != 1280 {
		t.Fatal("unexpected display dimensions")
	}
}

func TestLiveControllerRejectsInvalidToken(t *testing.T) {
	valid := liveController(t)
	address := valid.conn.Target()
	_, portText, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	invalid, err := DialController(port, "invalid-test-token")
	if err != nil {
		t.Fatal(err)
	}
	defer invalid.Close()
	_, err = invalid.Capture(nil)
	if status.Code(err) != codes.Unauthenticated && status.Code(err) != codes.PermissionDenied {
		t.Fatalf("invalid token did not fail authentication: %v", err)
	}
}

func TestLiveCaptureOrientation(t *testing.T) {
	c := liveController(t)
	frame, err := c.Capture(nil)
	if err != nil {
		t.Fatal(err)
	}
	adb := filepath.Join(os.Getenv("LOCALAPPDATA"), "Android", "Sdk", "platform-tools", "adb.exe")
	data, err := exec.Command(adb, "-s", "emulator-5578", "exec-out", "screencap", "-p").Output()
	if err != nil {
		t.Fatal(err)
	}
	reference, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	w, h := frame.Bounds().Dx(), frame.Bounds().Dy()
	if reference.Bounds().Dx() != w || reference.Bounds().Dy() != h {
		t.Fatal("ADB and gRPC display sizes differ")
	}
	var direct, flipped uint64
	for y := h / 16; y < h; y += h / 8 {
		for x := w / 16; x < w; x += w / 8 {
			r, g, b, _ := reference.At(x, y).RGBA()
			ar, ag, ab, _ := frame.At(x, y).RGBA()
			br, bg, bb, _ := frame.At(x, h-1-y).RGBA()
			delta := func(a, b uint32) uint64 {
				if a > b {
					return uint64(a - b)
				}
				return uint64(b - a)
			}
			direct += delta(r, ar) + delta(g, ag) + delta(b, ab)
			flipped += delta(r, br) + delta(g, bg) + delta(b, bb)
		}
	}
	t.Logf("reference difference: direct=%d vertical-flip=%d", direct, flipped)
	if direct > flipped {
		t.Fatal("gRPC image appears vertically inverted")
	}
}
