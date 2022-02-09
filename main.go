package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"syscall"
	"time"
)

var srv *http.Server

func main() {
	log.Println("Starting server...")
	mux := http.NewServeMux()
	mux.HandleFunc("/patch", handlePatch)
	mux.Handle("/", http.FileServer(http.Dir(".")))
	srv = &http.Server{
		Addr:    fmt.Sprintf(":%s", os.Getenv("PORT")),
		Handler: mux,
	}
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("FATAL: Failed to run server: %v", err)
	}
	if err := restart(); err != nil {
		log.Fatalf("FATAL: Failed to restart: %v", err)
	}
}

func restart() error {
	goPath, err := exec.LookPath("go")
	if err != nil {
		return fmt.Errorf("finding go executable: %v", err)
	}
	if err := syscall.Exec(goPath, []string{"go", "run", "."}, os.Environ()); err != nil {
		return fmt.Errorf("calling exec 'go run .': %v", err)
	}
	return nil
}

func handlePatch(rw http.ResponseWriter, r *http.Request) {
	if err := tryPatch(r.Body); err != nil {
		log.Printf("Failed trying patch: %v", err)
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	http.Error(rw, http.StatusText(http.StatusOK), http.StatusOK)
}

func tryPatch(r io.Reader) error {
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
	go stopServer()
	return nil
}

func stopServer() {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	log.Println("Stopping server...")
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("Failed to gracefully shutdown server: %v", err)
		log.Println("Stopping server forcefully...")
		if err := srv.Close(); err != nil {
			log.Fatalf("FATAL: Failed to stop server forcefully: %v", err)
		}
	}
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
	if err := applyPatchCmd.Run(); err != nil {
		return fmt.Errorf("applying patch to test server: %v", err)
	}

	runTestServerCmd := exec.Command("go", "run", ".")
	runTestServerCmd.Dir = testDir
	runTestServerCmd.Env = append(os.Environ(), "PORT=8081")
	if err := runTestServerCmd.Start(); err != nil {
		return fmt.Errorf("starting test server: %v", err)
	}
	defer func() {
		_ = runTestServerCmd.Process.Kill()
	}()

	passed := false
	for i := 0; i < 10; i++ {
		time.Sleep(1 * time.Second)
		resp, err := http.Get("http://localhost:8081/")
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
	cmd := exec.Command("git", "am")
	cmd.Stdin = bytes.NewReader(patch)
	b, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("applying patch: %v\n%s\n", err, string(b))
	}
	for _, line := range bytes.Split(bytes.TrimSpace(b), []byte("\n")) {
		log.Printf("git am: %s", line)
	}
	return nil
}
