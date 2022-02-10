package shell

import (
	"bufio"
	"bytes"
	md52 "crypto/md5"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

const ModeVar = "CHILD"

var (
	testPort, childPort int
)

func Run() error {
	log.SetPrefix("shell: ")
	log.SetFlags(0)
	shellPortStr := os.Getenv("PORT")
	port, err := strconv.Atoi(shellPortStr)
	if err != nil {
		return fmt.Errorf("converting port '%s'to int: %v", shellPortStr, err)
	}

	testPort, err = getFreePortAfter(port)
	if err != nil {
		return fmt.Errorf("finding free test port: %v", err)
	}
	childPort, err = getFreePortAfter(testPort)
	if err != nil {
		return fmt.Errorf("finding free child port: %v", err)
	}
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

func getFreePortAfter(port int) (int, error) {
	for p := port + 1; p < port+1000; p++ {
		_, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", p))
		if err != nil {
			return p, nil
		}
	}
	return 0, fmt.Errorf("could not find port")
}

func runChildServer() {
	err := runChildServerWithError()
	log.Fatalf("FATAL: %v", err)
}

func runChildServerWithError() error {
	d, err := ioutil.TempDir("", "")
	if err != nil {
		return fmt.Errorf("creating temp dir for build output: %v", err)
	}
	const base = "sovereign"
	bin := filepath.Join(d, base)
	if b, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		return fmt.Errorf("go build failed: %v\n%s", err, b)
	}
	cmd := exec.Command(bin)
	w := getPrefixedLogger("child: ")
	cmd.Stdout = w
	cmd.Stderr = w
	cmd.Env = append(os.Environ(), fmt.Sprintf("%s=true", ModeVar), fmt.Sprintf("PORT=%d", childPort))
	err = cmd.Run()
	code := cmd.ProcessState.ExitCode()
	return fmt.Errorf("child server stopped running: %d %v", code, err)
}

func getPrefixedLogger(prefix string) *io.PipeWriter {
	r, w := io.Pipe()
	go func() {
		scan := bufio.NewScanner(r)
		for scan.Scan() {
			log.Printf("%s%s", prefix, scan.Text())
		}
		log.Fatalf("FATAL: stopped reading child server logs: %v", scan.Err())
	}()
	return w
}

func handlePatch(rw http.ResponseWriter, r *http.Request) {
	if err := tryPatch(r.Body); err != nil {
		log.Printf("Failed trying patch: %v", err)
		http.Error(rw, fmt.Sprintf("ERROR: %v", err), http.StatusBadRequest)
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
	if err := applyPatch(patch, childPort); err != nil {
		return fmt.Errorf("applying patch: %v", err)
	}
	return nil
}

// Verify by spinning up a copy of this server, applying the patch, then applying the revert of the patch, and verifying
// you get the same server back as when the test was started.
func verifyPatch(patch []byte) error {
	var testDir string
	{
		tmpDir, err := ioutil.TempDir("", "")
		if err != nil {
			return fmt.Errorf("creating temporary test directory: %v", err)
		}
		testDir = filepath.Join(tmpDir, "test") // ensure destination does not exist yet so `clone` does not nest
	}

	// start test server
	var serverPID int
	{
		if err := exec.Command("git", "clone", ".git", testDir).Run(); err != nil {
			return fmt.Errorf("cloning current server to test server: %v", err)
		}

		tmpDir, err := ioutil.TempDir("", "")
		if err != nil {
			return fmt.Errorf("creating temporary test directory: %v", err)
		}
		bin := filepath.Join(tmpDir, "sovereign")
		buildCmd := exec.Command("go", "build", "-o", bin, ".")
		buildCmd.Dir = testDir
		if b, err := buildCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("error building test server: %v\n%s", err, b)
		}

		runTestServerCmd := exec.Command(bin)
		runTestServerCmd.Dir = testDir
		runTestServerCmd.Env = append(os.Environ(), fmt.Sprintf("%s=true", ModeVar), fmt.Sprintf("PORT=%d", testPort))
		runTestServerCmd.SysProcAttr = &syscall.SysProcAttr{
			Setpgid: true,
		}
		w := getPrefixedLogger("test:  ")
		runTestServerCmd.Stderr = w
		runTestServerCmd.Stdout = w
		if err := runTestServerCmd.Start(); err != nil {
			return fmt.Errorf("starting test server: %v", err)
		}
		defer func() {
			// kill entire subprocess group to account for future shells
			_ = syscall.Kill(-runTestServerCmd.Process.Pid, syscall.SIGKILL)
			// safe to ignore response body leak because if it's present the process is exiting anyway
			_, err := http.Get(fmt.Sprintf("http://localhost:%d/", testPort))
			if err == nil {
				log.Fatal("FATAL: Failed to stop test server.")
			}
		}()
		serverPID = runTestServerCmd.Process.Pid
	}

	// hash initial version of test server
	var initialServerHash string
	{
		var err error
		initialServerHash, err = hash(serverPID)
		if err != nil {
			return fmt.Errorf("hashing initial test server: %v", err)
		}
	}

	// wait for test server to be active
	if err := waitForTestServer(); err != nil {
		return fmt.Errorf("waiting for initial test server: %v", err)
	}

	// apply patch
	if err := applyPatch(patch, testPort); err != nil {
		return fmt.Errorf("applying patch to test server: %v", err)
	}

	// wait for test server to be active after patch
	if err := waitForTestServer(); err != nil {
		return fmt.Errorf("waiting for patched test server: %v", err)
	}

	// verify the hash is now different after letting the server update itself
	if patchedServerHash, err := hash(serverPID); err != nil {
		return fmt.Errorf("hashing patched test server: %v", err)
	} else if patchedServerHash == initialServerHash {
		return fmt.Errorf("test server did not update itself after being patched")
	}

	// get revert of the patch
	var revert []byte
	{
		var copyDir string
		{
			tmpDir, err := ioutil.TempDir("", "")
			if err != nil {
				return fmt.Errorf("creating temporary copy directory: %v", err)
			}
			copyDir = filepath.Join(tmpDir, "copy")
		}

		for _, args := range [][]string{
			{"cp", "-r", testDir, copyDir},
			{"git", "-C", copyDir, "revert", "--no-edit", "HEAD"},
		} {
			b, err := exec.Command(args[0], args[1:]...).CombinedOutput()
			if err != nil {
				return fmt.Errorf("getting revert of patch running '%v': %v\n%s", args, err, b)
			}
		}
		var err error
		revert, err = exec.Command("git", "-C", copyDir, "format-patch", "--stdout", "HEAD~1").CombinedOutput()
		if err != nil {
			return fmt.Errorf("getting patch for revert: %v", err)
		}
	}

	// apply revert of the patch
	if err := applyPatch(revert, testPort); err != nil {
		return fmt.Errorf("applying revert patch to test server: %v", err)
	}

	// wait for test server to be active after revert
	if err := waitForTestServer(); err != nil {
		return fmt.Errorf("waiting for reverted test server: %v", err)
	}

	// hash server after the revert
	finalServerHash, err := hash(serverPID)
	if err != nil {
		return fmt.Errorf("hashing reverted test server: %v", err)
	}

	// compare before and after
	if initialServerHash != finalServerHash {
		return fmt.Errorf("server is different after applying patch and reverting: %s %s", initialServerHash, finalServerHash)
	}

	return nil
}

func waitForTestServer() error {
	var last error
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		resp, err := http.Get(fmt.Sprintf("http://localhost:%d", testPort))
		if err == nil {
			flush(resp.Body)
			return nil
		}
		last = err
	}
	return fmt.Errorf("test server did not respond: %v", last)
}

func hash(pid int) (string, error) {
	path, err := getExePathFromPid(pid)
	if err != nil {
		return "", fmt.Errorf("getting exe path from initial test server pid: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("opening path '%s' to hash: %v", path, err)
	}
	defer func() {
		_ = f.Close()
	}()
	h := md52.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hashing '%s': %v", path, err)
	}
	hex := fmt.Sprintf("%x", h.Sum(nil))
	log.Println(hex, path)
	return hex, nil
}

func applyPatch(patch []byte, port int) error {
	childPatchURL := fmt.Sprintf("http://localhost:%d/patch", port)
	resp, err := http.Post(childPatchURL, "text/plain", bytes.NewReader(patch))
	if err != nil {
		return fmt.Errorf("posting patch to child server: %v", err)
	}
	defer flush(resp.Body)
	if resp.StatusCode != 200 {
		body, _ := ioutil.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("unexpected status code from child server: %s\n%s", resp.Status, body)
	}
	return nil
}

func flush(body io.ReadCloser) {
	_, _ = io.Copy(ioutil.Discard, body)
	_ = body.Close()
}
