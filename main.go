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

func verifyPatch(patch []byte) error {
	return nil // always accept any patch
}

func applyPatch(patch []byte) error {
	f, err := os.OpenFile("patches.log", os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		return fmt.Errorf("opening patches.log: %v", err)
	}
	if _, err := io.Copy(f, bytes.NewReader(patch)); err != nil {
		return fmt.Errorf("copying patch to patches.log: %v", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing patches.log: %v", err)
	}
	return nil
}
