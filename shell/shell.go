package shell

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

var (
	testPort, childPort int
)

func Run() error {
	log.SetPrefix("shell: ")
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	shellPortStr := os.Getenv("PORT")
	port, err := strconv.Atoi(shellPortStr)
	if err != nil {
		return fmt.Errorf("converting port '%s'to int: %v", shellPortStr, err)
	}
	testPort = port + 1
	childPort = port + 2
	log.Println("Starting child server...")
	go runChildServer()
	childURL, err := url.Parse(fmt.Sprintf("http://localhost:%d/", childPort))
	if err != nil {
		return fmt.Errorf("creating child url: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/patch", handlePatch)
	mux.Handle("/", httputil.NewSingleHostReverseProxy(childURL))
	log.Println("Starting shell server...")
	return http.ListenAndServe(":"+shellPortStr, mux)
}

func runChildServer() {
	cmd := exec.Command("go", "run", ".")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "ROLE=child", fmt.Sprintf("PORT=%d", childPort))
	err := cmd.Run()
	code := cmd.ProcessState.ExitCode()
	log.Fatalf("FATAL: child server stopped running: %d %v", code, err)
}

func handlePatch(rw http.ResponseWriter, r *http.Request) {
	if err := tryPatch(r.Body); err != nil {
		log.Printf("Failed trying patch: %v", err)
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	http.Error(rw, http.StatusText(http.StatusOK), http.StatusOK)
}

func tryPatch(r io.ReadCloser) error {
	patch, err := ioutil.ReadAll(r)
	if err != nil {
		return fmt.Errorf("reading patch: %v", err)
	}
	if err := verifyPatch(patch); err != nil {
		return fmt.Errorf("verifying patch: %v", err)
	}
	if err := applyPatch(patch); err != nil {
		return fmt.Errorf("applying patch: %v", err)
	}
	return nil
}

// Verify by spinning up a copy of the future server that would exist after this patch is applied and making sure it
// starts.
func verifyPatch(patch []byte) error {
	const testDir = "../test"
	if err := os.RemoveAll(testDir); err != nil {
		return fmt.Errorf("ensuring old test server is removed: %v", err)
	}
	defer func() {
		_ = os.RemoveAll(testDir)
	}()

	if err := exec.Command("git", "clone", ".git", testDir).Run(); err != nil {
		return fmt.Errorf("cloning current server to test server: %v", err)
	}

	applyPatchCmd := exec.Command("git", "am")
	applyPatchCmd.Dir = testDir
	applyPatchCmd.Stdin = bytes.NewReader(patch)
	if b, err := applyPatchCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("applying patch to test server: %v\n%s", err, b)
	}

	runTestServerCmd := exec.Command("go", "run", ".")
	runTestServerCmd.Dir = testDir
	runTestServerCmd.Env = append(os.Environ(), "ROLE=child", fmt.Sprintf("PORT=%d", testPort))
	runTestServerCmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	if err := runTestServerCmd.Start(); err != nil {
		return fmt.Errorf("starting test server: %v", err)
	}
	defer func() {
		// kill entire subprocess group to account for `go run .` being killed not propagating to subprocesses which
		// may leave the test server running
		_ = syscall.Kill(-runTestServerCmd.Process.Pid, syscall.SIGKILL)
		// safe to ignore response body leak because if it's present the process is exiting anyway
		_, err := http.Get(fmt.Sprintf("http://localhost:%d/", testPort))
		if err == nil {
			log.Fatal("FATAL: Failed to stop test server.")
		}
	}()

	passed := false
	for i := 0; i < 10; i++ {
		time.Sleep(1 * time.Second)
		resp, err := http.Get(fmt.Sprintf("http://localhost:%d/", testPort))
		if err != nil {
			log.Printf("Error while waiting for test server to respond: %v", err)
			continue
		}
		if resp.StatusCode == 200 {
			passed = true
		} else {
			log.Printf("Received bad status code from test server: %v", resp.Status)
		}
		_, _ = io.Copy(ioutil.Discard, resp.Body)
		_ = resp.Body.Close()
		if passed {
			break
		}
	}

	if !passed {
		return fmt.Errorf("timed out waiting for test server to successfully start")
	}
	log.Println("Verified the test server can start successfully.")
	return nil
}

func applyPatch(patch []byte) error {
	childPatchURL := fmt.Sprintf("http://localhost:%d/patch", childPort)
	resp, err := http.Post(childPatchURL, "text/plain", bytes.NewReader(patch))
	if err != nil {
		return fmt.Errorf("posting patch to child server: %v", err)
	}
	defer func() {
		_, _ = io.Copy(ioutil.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != 200 {
		body, _ := ioutil.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("unexpected status code from child server: %s\n%s", resp.Status, body)
	}
	return nil
}
