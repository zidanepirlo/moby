// +build !test_no_exec

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/docker/docker/integration-cli/checker"
	"github.com/docker/docker/internal/test/request"
	"github.com/go-check/check"
)

// Regression test for #9414
func (s *DockerSuite) TestExecAPICreateNoCmd(c *check.C) {
	name := "exec_test"
	dockerCmd(c, "run", "-d", "-t", "--name", name, "busybox", "/bin/sh")

	res, body, err := request.Post(fmt.Sprintf("/containers/%s/exec", name), request.JSONBody(map[string]interface{}{"Cmd": nil}))
	c.Assert(err, checker.IsNil)
	c.Assert(res.StatusCode, checker.Equals, http.StatusBadRequest)

	b, err := request.ReadBody(body)
	c.Assert(err, checker.IsNil)

	comment := check.Commentf("Expected message when creating exec command with no Cmd specified")
	c.Assert(getErrorMessage(c, b), checker.Contains, "No exec command specified", comment)
}

func (s *DockerSuite) TestExecAPICreateNoValidContentType(c *check.C) {
	name := "exec_test"
	dockerCmd(c, "run", "-d", "-t", "--name", name, "busybox", "/bin/sh")

	jsonData := bytes.NewBuffer(nil)
	if err := json.NewEncoder(jsonData).Encode(map[string]interface{}{"Cmd": nil}); err != nil {
		c.Fatalf("Can not encode data to json %s", err)
	}

	res, body, err := request.Post(fmt.Sprintf("/containers/%s/exec", name), request.RawContent(ioutil.NopCloser(jsonData)), request.ContentType("test/plain"))
	c.Assert(err, checker.IsNil)
	c.Assert(res.StatusCode, checker.Equals, http.StatusBadRequest)

	b, err := request.ReadBody(body)
	c.Assert(err, checker.IsNil)

	comment := check.Commentf("Expected message when creating exec command with invalid Content-Type specified")
	c.Assert(getErrorMessage(c, b), checker.Contains, "Content-Type specified", comment)
}

func (s *DockerSuite) TestExecAPICreateContainerPaused(c *check.C) {
	// Not relevant on Windows as Windows containers cannot be paused
	testRequires(c, DaemonIsLinux)
	name := "exec_create_test"
	dockerCmd(c, "run", "-d", "-t", "--name", name, "busybox", "/bin/sh")

	dockerCmd(c, "pause", name)

	cli, err := client.NewEnvClient()
	c.Assert(err, checker.IsNil)
	defer cli.Close()

	config := types.ExecConfig{
		Cmd: []string{"true"},
	}
	_, err = cli.ContainerExecCreate(context.Background(), name, config)

	comment := check.Commentf("Expected message when creating exec command with Container %s is paused", name)
	c.Assert(err.Error(), checker.Contains, "Container "+name+" is paused, unpause the container before exec", comment)
}

func (s *DockerSuite) TestExecAPIStart(c *check.C) {
	testRequires(c, DaemonIsLinux) // Uses pause/unpause but bits may be salvageable to Windows to Windows CI
	dockerCmd(c, "run", "-d", "--name", "test", "busybox", "top")

	id := createExec(c, "test")
	startExec(c, id, http.StatusOK)

	var execJSON struct{ PID int }
	inspectExec(c, id, &execJSON)
	c.Assert(execJSON.PID, checker.GreaterThan, 1)

	id = createExec(c, "test")
	dockerCmd(c, "stop", "test")

	startExec(c, id, http.StatusNotFound)

	dockerCmd(c, "start", "test")
	startExec(c, id, http.StatusNotFound)

	// make sure exec is created before pausing
	id = createExec(c, "test")
	dockerCmd(c, "pause", "test")
	startExec(c, id, http.StatusConflict)
	dockerCmd(c, "unpause", "test")
	startExec(c, id, http.StatusOK)
}

func (s *DockerSuite) TestExecAPIStartEnsureHeaders(c *check.C) {
	testRequires(c, DaemonIsLinux)
	dockerCmd(c, "run", "-d", "--name", "test", "busybox", "top")

	id := createExec(c, "test")
	resp, _, err := request.Post(fmt.Sprintf("/exec/%s/start", id), request.RawString(`{"Detach": true}`), request.JSON)
	c.Assert(err, checker.IsNil)
	c.Assert(resp.Header.Get("Server"), checker.Not(checker.Equals), "")
}

func (s *DockerSuite) TestExecAPIStartBackwardsCompatible(c *check.C) {
	testRequires(c, DaemonIsLinux) // Windows only supports 1.25 or later
	runSleepingContainer(c, "-d", "--name", "test")
	id := createExec(c, "test")

	resp, body, err := request.Post(fmt.Sprintf("/v1.20/exec/%s/start", id), request.RawString(`{"Detach": true}`), request.ContentType("text/plain"))
	c.Assert(err, checker.IsNil)

	b, err := request.ReadBody(body)
	comment := check.Commentf("response body: %s", b)
	c.Assert(err, checker.IsNil, comment)
	c.Assert(resp.StatusCode, checker.Equals, http.StatusOK, comment)
}

// #19362
func (s *DockerSuite) TestExecAPIStartMultipleTimesError(c *check.C) {
	runSleepingContainer(c, "-d", "--name", "test")
	execID := createExec(c, "test")
	startExec(c, execID, http.StatusOK)
	waitForExec(c, execID)

	startExec(c, execID, http.StatusConflict)
}

// #20638
func (s *DockerSuite) TestExecAPIStartWithDetach(c *check.C) {
	name := "foo"
	runSleepingContainer(c, "-d", "-t", "--name", name)

	config := types.ExecConfig{
		Cmd:          []string{"true"},
		AttachStderr: true,
	}

	cli, err := client.NewEnvClient()
	c.Assert(err, checker.IsNil)
	defer cli.Close()

	createResp, err := cli.ContainerExecCreate(context.Background(), name, config)
	c.Assert(err, checker.IsNil)

	_, body, err := request.Post(fmt.Sprintf("/exec/%s/start", createResp.ID), request.RawString(`{"Detach": true}`), request.JSON)
	c.Assert(err, checker.IsNil)

	b, err := request.ReadBody(body)
	comment := check.Commentf("response body: %s", b)
	c.Assert(err, checker.IsNil, comment)

	resp, _, err := request.Get("/_ping")
	c.Assert(err, checker.IsNil)
	if resp.StatusCode != http.StatusOK {
		c.Fatal("daemon is down, it should alive")
	}
}

// #30311
func (s *DockerSuite) TestExecAPIStartValidCommand(c *check.C) {
	name := "exec_test"
	dockerCmd(c, "run", "-d", "-t", "--name", name, "busybox", "/bin/sh")

	id := createExecCmd(c, name, "true")
	startExec(c, id, http.StatusOK)

	waitForExec(c, id)

	var inspectJSON struct{ ExecIDs []string }
	inspectContainer(c, name, &inspectJSON)

	c.Assert(inspectJSON.ExecIDs, checker.IsNil)
}

// #30311
func (s *DockerSuite) TestExecAPIStartInvalidCommand(c *check.C) {
	name := "exec_test"
	dockerCmd(c, "run", "-d", "-t", "--name", name, "busybox", "/bin/sh")

	id := createExecCmd(c, name, "invalid")
	startExec(c, id, http.StatusBadRequest)
	waitForExec(c, id)

	var inspectJSON struct{ ExecIDs []string }
	inspectContainer(c, name, &inspectJSON)

	c.Assert(inspectJSON.ExecIDs, checker.IsNil)
}

func (s *DockerSuite) TestExecStateCleanup(c *check.C) {
	testRequires(c, DaemonIsLinux, SameHostDaemon)

	// This test checks accidental regressions. Not part of stable API.

	name := "exec_cleanup"
	cid, _ := dockerCmd(c, "run", "-d", "-t", "--name", name, "busybox", "/bin/sh")
	cid = strings.TrimSpace(cid)

	stateDir := "/var/run/docker/containerd/" + cid

	checkReadDir := func(c *check.C) (interface{}, check.CommentInterface) {
		fi, err := ioutil.ReadDir(stateDir)
		c.Assert(err, checker.IsNil)
		return len(fi), nil
	}

	fi, err := ioutil.ReadDir(stateDir)
	c.Assert(err, checker.IsNil)
	c.Assert(len(fi), checker.GreaterThan, 1)

	id := createExecCmd(c, name, "ls")
	startExec(c, id, http.StatusOK)
	waitForExec(c, id)

	waitAndAssert(c, 5*time.Second, checkReadDir, checker.Equals, len(fi))

	id = createExecCmd(c, name, "invalid")
	startExec(c, id, http.StatusBadRequest)
	waitForExec(c, id)

	waitAndAssert(c, 5*time.Second, checkReadDir, checker.Equals, len(fi))

	dockerCmd(c, "stop", name)
	_, err = os.Stat(stateDir)
	c.Assert(err, checker.NotNil)
	c.Assert(os.IsNotExist(err), checker.True)
}

func createExec(c *check.C, name string) string {
	return createExecCmd(c, name, "true")
}

func createExecCmd(c *check.C, name string, cmd string) string {
	_, reader, err := request.Post(fmt.Sprintf("/containers/%s/exec", name), request.JSONBody(map[string]interface{}{"Cmd": []string{cmd}}))
	c.Assert(err, checker.IsNil)
	b, err := ioutil.ReadAll(reader)
	c.Assert(err, checker.IsNil)
	defer reader.Close()
	createResp := struct {
		ID string `json:"Id"`
	}{}
	c.Assert(json.Unmarshal(b, &createResp), checker.IsNil, check.Commentf(string(b)))
	return createResp.ID
}

func startExec(c *check.C, id string, code int) {
	resp, body, err := request.Post(fmt.Sprintf("/exec/%s/start", id), request.RawString(`{"Detach": true}`), request.JSON)
	c.Assert(err, checker.IsNil)

	b, err := request.ReadBody(body)
	comment := check.Commentf("response body: %s", b)
	c.Assert(err, checker.IsNil, comment)
	c.Assert(resp.StatusCode, checker.Equals, code, comment)
}

func inspectExec(c *check.C, id string, out interface{}) {
	resp, body, err := request.Get(fmt.Sprintf("/exec/%s/json", id))
	c.Assert(err, checker.IsNil)
	defer body.Close()
	c.Assert(resp.StatusCode, checker.Equals, http.StatusOK)
	err = json.NewDecoder(body).Decode(out)
	c.Assert(err, checker.IsNil)
}

func waitForExec(c *check.C, id string) {
	timeout := time.After(60 * time.Second)
	var execJSON struct{ Running bool }
	for {
		select {
		case <-timeout:
			c.Fatal("timeout waiting for exec to start")
		default:
		}

		inspectExec(c, id, &execJSON)
		if !execJSON.Running {
			break
		}
	}
}

func inspectContainer(c *check.C, id string, out interface{}) {
	resp, body, err := request.Get(fmt.Sprintf("/containers/%s/json", id))
	c.Assert(err, checker.IsNil)
	defer body.Close()
	c.Assert(resp.StatusCode, checker.Equals, http.StatusOK)
	err = json.NewDecoder(body).Decode(out)
	c.Assert(err, checker.IsNil)
}
