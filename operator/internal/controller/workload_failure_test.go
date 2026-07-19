package controller

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func waitingPod(reason string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-marc"},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:  "desktop",
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reason}},
			}},
		},
	}
}

func TestPodFailure(t *testing.T) {
	crashLoopOOM := waitingPod("CrashLoopBackOff")
	crashLoopOOM.Status.ContainerStatuses[0].RestartCount = 6
	crashLoopOOM.Status.ContainerStatuses[0].LastTerminationState = corev1.ContainerState{
		Terminated: &corev1.ContainerStateTerminated{ExitCode: 137, Reason: "OOMKilled"},
	}

	initPull := &corev1.Pod{
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			InitContainerStatuses: []corev1.ContainerStatus{{
				Name:  "init",
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}},
			}},
		},
	}

	evicted := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-marc"},
		Status: corev1.PodStatus{
			Phase:   corev1.PodFailed,
			Reason:  "Evicted",
			Message: "node was low on resource: memory",
		},
	}

	cases := []struct {
		name       string
		pod        *corev1.Pod
		wantReason string // "" = no failure expected
		wantInMsg  string
	}{
		{name: "crash loop", pod: waitingPod("CrashLoopBackOff"), wantReason: "CrashLoopBackOff"},
		{name: "image pull backoff", pod: waitingPod("ImagePullBackOff"), wantReason: "ImagePullBackOff"},
		{name: "err image pull", pod: waitingPod("ErrImagePull"), wantReason: "ErrImagePull"},
		{name: "config error", pod: waitingPod("CreateContainerConfigError"), wantReason: "CreateContainerConfigError"},
		{name: "create container error", pod: waitingPod("CreateContainerError"), wantReason: "CreateContainerError"},
		{name: "invalid image name", pod: waitingPod("InvalidImageName"), wantReason: "InvalidImageName"},
		{name: "run container error", pod: waitingPod("RunContainerError"), wantReason: "RunContainerError"},
		{name: "container creating is normal", pod: waitingPod("ContainerCreating")},
		{name: "pod initializing is normal", pod: waitingPod("PodInitializing")},
		{name: "no statuses yet is normal", pod: &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodPending}}},
		{name: "evicted pod", pod: evicted, wantReason: "Evicted", wantInMsg: "low on resource"},
		{name: "failed pod without reason", pod: &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodFailed}}, wantReason: "PodFailed"},
		{name: "init container pull failure", pod: initPull, wantReason: "ImagePullBackOff", wantInMsg: `container "init"`},
		{name: "crash loop carries last termination", pod: crashLoopOOM, wantReason: "CrashLoopBackOff", wantInMsg: "137, OOMKilled, 6 restarts"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := podFailure(tc.pod)
			if tc.wantReason == "" {
				if got != nil {
					t.Fatalf("expected no failure, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected failure %s, got nil", tc.wantReason)
			}
			if got.Reason != tc.wantReason {
				t.Fatalf("expected reason %s, got %s (%s)", tc.wantReason, got.Reason, got.Message)
			}
			if tc.wantInMsg != "" && !strings.Contains(got.Message, tc.wantInMsg) {
				t.Fatalf("expected message to contain %q, got %q", tc.wantInMsg, got.Message)
			}
		})
	}
}
