package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/neko233-com/NekoSimulator233/internal/avd"
	"github.com/neko233-com/NekoSimulator233/internal/testutil"
)

func TestJoystickRejectsInvalidSequenceBeforeOpeningInstance(t *testing.T) {
	s := &Service{Manager: func() (avd.Manager, error) {
		t.Fatal("invalid joystick request opened an instance")
		return avd.Manager{}, nil
	}}
	for _, input := range []JoystickSequence{
		{},
		{Steps: []JoystickStep{{DX: 2, DurationMS: 100}}},
		{Steps: []JoystickStep{{DY: -2, DurationMS: 100}}},
		{Steps: []JoystickStep{{DurationMS: 49}}},
		{Steps: []JoystickStep{{DurationMS: 5001}}},
		{Steps: make([]JoystickStep, 33)},
		{Steps: []JoystickStep{{DurationMS: 5000}, {DurationMS: 5000}, {DurationMS: 5000}, {DurationMS: 5000}, {DurationMS: 50}}},
	} {
		if _, _, err := s.joystick(context.Background(), nil, input); err == nil {
			t.Fatalf("accepted invalid joystick sequence: %+v", input)
		}
	}
}

func TestLiveMCP(t *testing.T) {
	home := os.Getenv("NEKO_TEST_AGENT_AVD_HOME")
	if home == "" {
		t.Skip("set NEKO_TEST_AGENT_AVD_HOME to the isolated validation AVD")
	}
	testutil.LockAVD(t, home)
	manager, err := avd.DiscoverManager()
	if err != nil {
		t.Fatal(err)
	}
	manager.Home = home
	manager.Name = "NekoValidation"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	server := New(func() (avd.Manager, error) { return manager, nil })
	client := mcp.NewClient(&mcp.Implementation{Name: "neko-integration-test", Version: "1.0.0"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	list, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) != 22 {
		t.Fatalf("got %d tools", len(list.Tools))
	}
	call := func(name string, args any) *mcp.CallToolResult {
		t.Helper()
		result, e := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if e != nil {
			t.Fatal(e)
		}
		if result.IsError {
			t.Fatalf("%s failed: %+v", name, result.Content)
		}
		return result
	}
	call("neko_status", map[string]any{})
	shot := call("neko_screenshot", map[string]any{})
	if len(shot.Content) != 1 {
		t.Fatal("screenshot returned no image")
	}
	img, ok := shot.Content[0].(*mcp.ImageContent)
	if !ok || img.MIMEType != "image/png" {
		t.Fatalf("unexpected image result: %T", shot.Content[0])
	}
	decoded, err := png.Decode(bytes.NewReader(img.Data))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds().Dx() != 720 || decoded.Bounds().Dy() != 1280 {
		t.Fatal("wrong screenshot dimensions")
	}
	call("neko_tap", map[string]any{"x": 100, "y": 100})
	call("neko_swipe", map[string]any{"x1": 300, "y1": 700, "x2": 300, "y2": 400, "duration_ms": 100})
	call("neko_joystick", map[string]any{"x": 300, "y": 600, "radius": 80, "steps": []map[string]int{{"dx": 0, "dy": -1, "duration_ms": 100}, {"dx": 1, "dy": -1, "duration_ms": 100}}})
	invalidStick, invalidStickErr := cs.CallTool(ctx, &mcp.CallToolParams{Name: "neko_joystick", Arguments: map[string]any{"x": 300, "y": 600, "radius": 80, "steps": []map[string]int{{"dx": 2, "dy": 0, "duration_ms": 100}}}})
	if invalidStickErr != nil || !invalidStick.IsError {
		t.Fatal("invalid joystick direction was not rejected")
	}
	call("neko_multi_swipe", map[string]any{"paths": []map[string]int{{"x1": 200, "y1": 600, "x2": 100, "y2": 600}, {"x1": 500, "y1": 600, "x2": 600, "y2": 600}}, "duration_ms": 200})
	invalidMulti, e := cs.CallTool(ctx, &mcp.CallToolParams{Name: "neko_multi_swipe", Arguments: map[string]any{"paths": []map[string]int{{"x1": 200, "y1": 600, "x2": 100, "y2": 600}, {"x1": 500, "y1": 600, "x2": 9999, "y2": 600}}, "duration_ms": 100}})
	if e != nil || !invalidMulti.IsError {
		t.Fatal("out-of-bounds multi-touch was not rejected")
	}
	multiWatchCtx, multiWatchCancel := context.WithTimeout(ctx, 15*time.Second)
	multiWatch := exec.CommandContext(multiWatchCtx, filepath.Join(manager.SDK, "platform-tools", "adb.exe"), "-s", "emulator-5578", "shell", "getevent", "-lt")
	multiPipe, e := multiWatch.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	if e = multiWatch.Start(); e != nil {
		t.Fatal(e)
	}
	multiDone := make(chan []byte, 1)
	go func() { data, _ := io.ReadAll(multiPipe); multiDone <- data }()
	time.Sleep(300 * time.Millisecond)
	multiCtx, multiCancel := context.WithTimeout(ctx, 2*time.Second)
	_, multiErr := cs.CallTool(multiCtx, &mcp.CallToolParams{Name: "neko_multi_swipe", Arguments: map[string]any{"paths": []map[string]int{{"x1": 200, "y1": 600, "x2": 100, "y2": 600}, {"x1": 500, "y1": 600, "x2": 600, "y2": 600}}, "duration_ms": 5000}})
	multiCancel()
	time.Sleep(500 * time.Millisecond)
	stickCtx, stickCancel := context.WithTimeout(ctx, 3*time.Second)
	_, stickErr := cs.CallTool(stickCtx, &mcp.CallToolParams{Name: "neko_joystick", Arguments: map[string]any{"x": 300, "y": 600, "radius": 80, "steps": []map[string]int{{"dx": 1, "dy": 1, "duration_ms": 5000}}}})
	stickCancel()
	time.Sleep(500 * time.Millisecond)
	multiWatchCancel()
	multiEvents := <-multiDone
	_ = multiWatch.Wait()
	if multiErr == nil {
		t.Fatal("expected multi-touch cancellation")
	}
	if stickErr == nil {
		t.Fatal("expected joystick cancellation")
	}
	released := 0
	for _, line := range strings.Split(string(multiEvents), "\n") {
		if strings.Contains(line, "ABS_MT_TRACKING_ID") && strings.HasSuffix(strings.TrimSpace(line), "ffffffff") {
			released++
		}
	}
	if released < 3 {
		t.Fatalf("canceled MCP gestures released %d fingers, want at least 3", released)
	}
	watchCtx, watchCancel := context.WithTimeout(ctx, 8*time.Second)
	watch := exec.CommandContext(watchCtx, filepath.Join(manager.SDK, "platform-tools", "adb.exe"), "-s", "emulator-5578", "shell", "getevent", "-lt")
	watchPipe, e := watch.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	if e = watch.Start(); e != nil {
		t.Fatal(e)
	}
	watchDone := make(chan []byte, 1)
	go func() { data, _ := io.ReadAll(watchPipe); watchDone <- data }()
	time.Sleep(300 * time.Millisecond)
	holdCtx, holdCancel := context.WithTimeout(ctx, 3*time.Second)
	_, holdErr := cs.CallTool(holdCtx, &mcp.CallToolParams{Name: "neko_hold_key", Arguments: map[string]any{"usb_usage": 26, "duration_ms": 10000}})
	holdCancel()
	if holdErr == nil {
		watchCancel()
		<-watchDone
		_ = watch.Wait()
		t.Fatal("expected hold cancellation")
	}
	time.Sleep(500 * time.Millisecond)
	watchCancel()
	keyEvents := <-watchDone
	_ = watch.Wait()
	var sawDown, sawUp bool
	for _, line := range strings.Split(string(keyEvents), "\n") {
		if strings.Contains(line, "KEY_W") {
			if strings.HasSuffix(strings.TrimSpace(line), "DOWN") {
				sawDown = true
			}
			if strings.HasSuffix(strings.TrimSpace(line), "UP") {
				sawUp = true
			}
		}
	}
	if !sawDown || !sawUp {
		t.Fatalf("canceled MCP hold did not release guest key: down=%v up=%v", sawDown, sawUp)
	}
	call("neko_ui_hierarchy", map[string]any{})
	call("neko_apps", map[string]any{})
	if apk := os.Getenv("NEKO_TEST_APK"); apk != "" {
		adb := filepath.Join(manager.SDK, "platform-tools", "adb.exe")
		pkg := "invalid.example.nekoagentfixture"
		before, e := exec.Command(adb, "-s", "emulator-5578", "shell", "pm", "list", "packages", pkg).Output()
		if e != nil || strings.TrimSpace(string(before)) != "" {
			t.Fatal("test fixture package already exists or could not be checked")
		}
		defer func() {
			output, _ := exec.Command(adb, "-s", "emulator-5578", "shell", "pm", "list", "packages", pkg).Output()
			if strings.TrimSpace(string(output)) == "package:"+pkg {
				_ = exec.Command(adb, "-s", "emulator-5578", "uninstall", pkg).Run()
			}
		}()
		call("neko_install_apk", map[string]any{"path": apk})
		installed := call("neko_apps", map[string]any{})
		found := false
		for _, part := range installed.Content {
			if text, ok := part.(*mcp.TextContent); ok && strings.Contains(text.Text, pkg) {
				found = true
			}
		}
		if !found {
			t.Fatal("installed test APK is missing from package list")
		}
		duplicate, e := cs.CallTool(ctx, &mcp.CallToolParams{Name: "neko_install_apk", Arguments: map[string]any{"path": apk}})
		if e != nil || !duplicate.IsError {
			t.Fatal("replacement without permission was not rejected")
		}
		call("neko_install_apk", map[string]any{"path": apk, "replace": true})
	}
	invalid, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "neko_tap", Arguments: map[string]any{"x": 9999, "y": 0}})
	if err != nil || !invalid.IsError {
		t.Fatal("out-of-bounds touch was not rejected")
	}
	adb := filepath.Join(manager.SDK, "platform-tools", "adb.exe")
	for _, pkg := range []string{"com.android.settings", "com.google.android.settings.intelligence"} {
		if err := exec.Command(adb, "-s", "emulator-5578", "shell", "am", "force-stop", pkg).Run(); err != nil {
			t.Fatal(err)
		}
	}
	if err := exec.Command(adb, "-s", "emulator-5578", "shell", "am", "start", "-a", "android.settings.SETTINGS").Run(); err != nil {
		t.Fatal(err)
	}
	var focusReady bool
	for attempt := 0; attempt < 8; attempt++ {
		out, err := exec.Command(adb, "-s", "emulator-5578", "exec-out", "uiautomator", "dump", "/dev/tty").Output()
		if err != nil {
			t.Fatal(err)
		}
		text := string(out)
		if strings.Contains(text, `id/open_search_view_edit_text"`) && strings.Contains(text, `focused="true"`) {
			focusReady = true
			break
		}
		if strings.Contains(text, `resource-id="com.android.settings:id/search_action_bar_title"`) {
			selector := map[string]any{"resource_id": "com.android.settings:id/search_action_bar_title"}
			found := call("neko_find_ui", selector)
			encoded, err := json.Marshal(found.StructuredContent)
			if err != nil || !strings.Contains(string(encoded), "com.android.settings:id/search_action_bar_title") {
				t.Fatalf("UI selector did not find Settings search: %s %v", encoded, err)
			}
			call("neko_tap_ui", selector)
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !focusReady {
		t.Fatal("could not focus the isolated Settings search field")
	}
	if err := exec.Command(adb, "-s", "emulator-5578", "shell", "input", "keycombination", "113", "29").Run(); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command(adb, "-s", "emulator-5578", "shell", "input", "keyevent", "67").Run(); err != nil {
		t.Fatal(err)
	}
	call("neko_text", map[string]any{"text": "Neko233"})
	var hierarchyText string
	for i := 0; i < 10; i++ {
		tree := call("neko_ui_hierarchy", map[string]any{})
		for _, part := range tree.Content {
			if text, ok := part.(*mcp.TextContent); ok {
				hierarchyText = text.Text
			}
		}
		if strings.Contains(hierarchyText, "Neko233") {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !strings.Contains(hierarchyText, "Neko233") {
		t.Fatal("typed text was not found in focused test field")
	}
	call("neko_text", map[string]any{"text": " a'%s;$(x)"})
	var specialText string
	for i := 0; i < 5; i++ {
		tree := call("neko_ui_hierarchy", map[string]any{})
		for _, part := range tree.Content {
			if text, ok := part.(*mcp.TextContent); ok {
				specialText = text.Text
			}
		}
		if strings.Contains(specialText, "Neko233 a'%s;$(x)") {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !strings.Contains(specialText, "Neko233 a'%s;$(x)") {
		t.Fatal("special ASCII input did not round-trip")
	}
	instance, err := manager.Ready(ctx)
	if err != nil {
		t.Fatal(err)
	}
	clip, err := instance.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer clip.Close()
	if err = clip.SetClipboard("neko-clipboard-before"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		value, e := clip.Clipboard()
		if e == nil && value == "neko-clipboard-before" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	call("neko_text", map[string]any{"text": "中文测试"})
	var chineseText string
	for i := 0; i < 5; i++ {
		tree := call("neko_ui_hierarchy", map[string]any{})
		for _, part := range tree.Content {
			if text, ok := part.(*mcp.TextContent); ok {
				chineseText = text.Text
			}
		}
		if strings.Contains(chineseText, "中文测试") {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !strings.Contains(chineseText, "中文测试") {
		t.Fatal("Unicode text was not pasted into focused test field")
	}
	for i := 0; i < 20; i++ {
		value, e := clip.Clipboard()
		if e == nil && value == "neko-clipboard-before" {
			break
		}
		if i == 19 {
			t.Fatal("guest clipboard was not restored")
		}
		time.Sleep(50 * time.Millisecond)
	}
	call("neko_key", map[string]any{"key": "Home"})
	var foreground string
	for i := 0; i < 15; i++ {
		out, _ := exec.Command(adb, "-s", "emulator-5578", "shell", "dumpsys", "activity", "activities").Output()
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "topResumedActivity") {
				foreground = line
				break
			}
		}
		if strings.Contains(foreground, "NexusLauncherActivity") {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !strings.Contains(foreground, "NexusLauncherActivity") {
		t.Fatal("Home key did not return test guest to launcher")
	}
	call("neko_launch_app", map[string]any{"package": "com.android.settings"})
	foreground = ""
	for i := 0; i < 15; i++ {
		out, _ := exec.Command(adb, "-s", "emulator-5578", "shell", "dumpsys", "activity", "activities").Output()
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "topResumedActivity") {
				foreground = line
				break
			}
		}
		if strings.Contains(foreground, "com.android.settings") {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !strings.Contains(foreground, "com.android.settings") {
		t.Fatal("app launch did not bring Settings to foreground")
	}
	call("neko_key", map[string]any{"key": "Home"})
	t.Log("MCP tools listed; real PNG, touch, swipe, text, accessibility tree, package list, app launch, bounds check and Home key verified on isolated instance")
}

func TestLiveMCPLifecycle(t *testing.T) {
	root := os.Getenv("NEKO_TEST_AGENT_LIFECYCLE_HOME")
	if root == "" {
		t.Skip("set NEKO_TEST_AGENT_LIFECYCLE_HOME to an unused isolated AVD directory")
	}
	manager, err := avd.DiscoverManager()
	if err != nil {
		t.Fatal(err)
	}
	manager.Home = root
	manager.Name = "NekoAgentLifecycle"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	server := New(func() (avd.Manager, error) { return manager, nil })
	client := mcp.NewClient(&mcp.Implementation{Name: "lifecycle-test", Version: "1.0"}, nil)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	t.Cleanup(func() {
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanCancel()
		_ = manager.Stop(cleanCtx)
	})
	call := func(name string) State {
		t.Helper()
		r, e := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
		if e != nil || r.IsError {
			t.Fatalf("%s failed: %v", name, e)
		}
		raw, e := json.Marshal(r.StructuredContent)
		if e != nil {
			t.Fatal(e)
		}
		var state State
		if e = json.Unmarshal(raw, &state); e != nil {
			t.Fatal(e)
		}
		return state
	}
	started := call("neko_start")
	if !started.Ready || !started.KeyboardEnabled || started.VulkanGPU == "" {
		t.Fatal("start did not report a ready Vulkan keyboard instance")
	}
	data := filepath.Join(root, manager.Name+".avd", "userdata-qemu.img")
	if _, err = os.Stat(data); err != nil {
		t.Fatal(err)
	}
	if state := call("neko_status"); !state.Ready || state.Serial != started.Serial {
		t.Fatal("status did not identify started instance")
	}
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "neko_stop", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("stop failed: %v", err)
	}
	if _, err = os.Stat(data); err != nil {
		t.Fatal("instance data lost during MCP stop")
	}
	if state := call("neko_status"); state.Ready {
		t.Fatal("stopped instance remained ready")
	}
	t.Log("MCP start, status, stop and persistent VM data verified")
}
