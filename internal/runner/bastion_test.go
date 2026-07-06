package runner_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/yousysadmin/whoosh/internal/runner"
	"github.com/yousysadmin/whoosh/transport/ssh"
	"github.com/yousysadmin/whoosh/transport/sshtest"
)

// TestCluster_Bastion runs targets through a shared jump host and verifies Close tears the bastion connection
// down with the rest of the pool.
func TestCluster_Bastion(t *testing.T) {
	bastionSrv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start bastion: %v", err)
	}
	defer bastionSrv.Close()
	targetSrv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start target: %v", err)
	}
	defer targetSrv.Close()

	b := ssh.NewBastion(ssh.Target{Host: bastionSrv.Host, Port: bastionSrv.Port, IdentityFile: bastionSrv.IdentityFile})
	c := runner.NewCluster(runner.Options{Bastion: b}, io.Discard)

	targets := []runner.Target{{Host: targetSrv.Host, Port: targetSrv.Port, User: "deploy", IdentityFile: targetSrv.IdentityFile}}
	if res := c.Run(context.Background(), targets, func(string) string { return "true" }, 0, true); runner.Failed(res) {
		t.Fatalf("run through bastion failed: %+v", res)
	}

	c.Close()

	// The cluster closed the shared bastion: a later dial through it must fail instead of reopening it.
	_, err = ssh.Dial(context.Background(),
		ssh.Target{Host: targetSrv.Host, Port: targetSrv.Port, IdentityFile: targetSrv.IdentityFile},
		ssh.Options{Bastion: b})
	if err == nil || !strings.Contains(err.Error(), "connection closed") {
		t.Errorf("dial through a closed bastion should fail with a closed error, got: %v", err)
	}
}
