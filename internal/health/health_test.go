package health

import (
	"slices"
	"strings"
	"testing"
)

// The lines below are `docker compose ps --all --format json` output, which
// Docker Compose prints as one JSON object per line. deadLine, oddStateLine
// and oddHealthLine were written by hand in the same format; all the others
// are real, from Docker Compose 5.6.0. All but fullLine are trimmed to a few
// fields and given the node's service names.
const (
	fullLine      = `{"Command":"\"/docker-entrypoint.…\"","CreatedAt":"2026-10-09 13:16:01 +0000 UTC","Engine":"","ExitCode":0,"Health":"healthy","ID":"770f8c3b6603","Image":"nginx:1.30.5-alpine@sha256:0985e772fb9f729e6fa0980da05fca5d9c468e870eed43071545afa9d2e27d94","Labels":"com.docker.compose.depends_on=api:service_healthy:false,com.docker.compose.image=sha256:8f84ed99befc3891b8f329c5c202785278a2cfb7c25107d57fb2a134a3117433,com.docker.compose.oneoff=False,com.docker.compose.project=titan,com.docker.compose.project.config_files=/opt/titan/compose.yaml,com.docker.compose.service=nginx,com.docker.compose.project.working_dir=/opt/titan,com.docker.compose.version=5.6.0,maintainer=NGINX Docker Maintainers \u003cdocker-maint@nginx.com\u003e,com.docker.compose.config-hash=b9b129d9d812183ec7c024225fd61386c7662aa4bf9da8ca105ba3f8ed24e736,com.docker.compose.container-number=1","LocalVolumes":"0","Mounts":"/opt/titan/ngi…,/opt/titan/ngi…,/opt/titan/ngi…","Name":"titan-nginx-1","Names":"titan-nginx-1","Networks":"titan_titan","Ports":"80/tcp, 0.0.0.0:443-\u003e8443/tcp, [::]:443-\u003e8443/tcp","Project":"titan","Publishers":[{"URL":"","TargetPort":80,"PublishedPort":0,"Protocol":"tcp"},{"URL":"0.0.0.0","TargetPort":8443,"PublishedPort":443,"Protocol":"tcp"},{"URL":"::","TargetPort":8443,"PublishedPort":443,"Protocol":"tcp"}],"RunningFor":"59 seconds ago","Service":"nginx","Size":"0B","State":"running","Status":"Up 45 seconds (healthy)"}`
	healthyLine   = `{"ExitCode":0,"Health":"healthy","Name":"titan-api-1","Service":"api","State":"running","Status":"Up 9 seconds (healthy)"}`
	runningLine   = `{"ExitCode":0,"Health":"","Name":"titan-controller-1","Service":"controller","State":"running","Status":"Up 9 seconds"}`
	startingLine  = `{"ExitCode":0,"Health":"starting","Name":"titan-nginx-1","Service":"nginx","State":"running","Status":"Up 3 seconds (health: starting)"}`
	unhealthyLine = `{"ExitCode":0,"Health":"unhealthy","Name":"titan-db-1","Service":"db","State":"running","Status":"Up 7 seconds (unhealthy)"}`
	doneLine      = `{"ExitCode":0,"Health":"","Name":"titan-migrate-1","Service":"migrate","State":"exited","Status":"Exited (0) 8 seconds ago"}`
	failedLine    = `{"ExitCode":3,"Health":"","Name":"titan-migrate-1","Service":"migrate","State":"exited","Status":"Exited (3) 7 seconds ago"}`
	restartLine   = `{"ExitCode":0,"Health":"","Name":"titan-api-1","Service":"api","State":"restarting","Status":"Restarting (1) 2 seconds ago"}`
	deadLine      = `{"ExitCode":137,"Health":"","Name":"titan-api-1","Service":"api","State":"dead","Status":"Dead"}`
	createdLine   = `{"ExitCode":0,"Health":"","Name":"titan-api-1","Service":"api","State":"created","Status":"Created"}`
	pausedLine    = `{"ExitCode":0,"Health":"","Name":"titan-api-1","Service":"api","State":"paused","Status":"Up 8 seconds (Paused)"}`
	oddStateLine  = `{"ExitCode":0,"Health":"","Name":"titan-api-1","Service":"api","State":"frozen","Status":"Frozen"}`
	oddHealthLine = `{"ExitCode":0,"Health":"sleepy","Name":"titan-api-1","Service":"api","State":"running","Status":"Up 9 seconds (sleepy)"}`
	noServiceLine = `{"ExitCode":0,"Health":"healthy","Name":"titan-api-1","State":"running","Status":"Up 9 seconds (healthy)"}`
)

// lines joins JSON lines the way compose prints them, each ending in a newline.
func lines(l ...string) []byte {
	return []byte(strings.Join(l, "\n") + "\n")
}

func TestParse(t *testing.T) {
	tests := []struct {
		name   string
		output []byte
		want   []Service
	}{
		{"a line with every field compose prints", lines(fullLine), []Service{{"nginx", Healthy}}},
		{"running and healthy is healthy", lines(healthyLine), []Service{{"api", Healthy}}},
		{"running without a healthcheck is running", lines(runningLine), []Service{{"controller", Running}}},
		{"running with its healthcheck starting is starting", lines(startingLine), []Service{{"nginx", Starting}}},
		{"running and unhealthy is unhealthy", lines(unhealthyLine), []Service{{"db", Unhealthy}}},
		{"exited with code 0 is done", lines(doneLine), []Service{{"migrate", Done}}},
		{"exited with another code is down", lines(failedLine), []Service{{"migrate", Down}}},
		{"restarting is down", lines(restartLine), []Service{{"api", Down}}},
		{"dead is down", lines(deadLine), []Service{{"api", Down}}},
		{"created is down", lines(createdLine), []Service{{"api", Down}}},
		{"paused is down", lines(pausedLine), []Service{{"api", Down}}},
		{"a state Docker may add later is unknown", lines(oddStateLine), []Service{{"api", Unknown}}},
		{"a health Docker may add later is unknown", lines(oddHealthLine), []Service{{"api", Unknown}}},
		{
			"several lines give the services in the order given",
			lines(startingLine, healthyLine, unhealthyLine, doneLine, runningLine),
			[]Service{
				{"nginx", Starting}, {"api", Healthy}, {"db", Unhealthy},
				{"migrate", Done}, {"controller", Running},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Parse(test.output)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !slices.Equal(got, test.want) {
				t.Errorf("Parse = %v, want %v", got, test.want)
			}
		})
	}
}

func TestParseEmptyOutputIsNoServices(t *testing.T) {
	got, err := Parse(nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Parse = %v, want no services", got)
	}
}

func TestParseReadsALineLongerThan64KiB(t *testing.T) {
	labels := strings.Repeat("x", 100<<10)
	line := `{"ExitCode":0,"Health":"healthy","Labels":"` + labels + `","Service":"api","State":"running"}`
	got, err := Parse(lines(line))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if want := []Service{{"api", Healthy}}; !slices.Equal(got, want) {
		t.Errorf("Parse = %v, want %v", got, want)
	}
}

func TestParseNamesTheLineThatIsNotJSON(t *testing.T) {
	_, err := Parse(lines(healthyLine, "not json", runningLine))
	if err == nil {
		t.Fatal("Parse succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error %q does not name line 2", err)
	}
}

func TestParseRefusesALineWithoutService(t *testing.T) {
	_, err := Parse(lines(noServiceLine))
	if err == nil {
		t.Fatal("Parse succeeded, want an error")
	}
}
