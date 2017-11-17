package autosnapshot

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	compute "google.golang.org/api/compute/v1"
	"google.golang.org/appengine"
	"google.golang.org/appengine/log"
	"google.golang.org/appengine/urlfetch"
)

const (
	label  = "autosnapshot"
	prefix = "autosnapshot"
)

type Disk struct {
	Id      uint64
	Name    string
	Status  string
	Zone    string
	KeepFor int
}

func init() {
	http.HandleFunc("/cron", cronHandler)
}

func cronHandler(w http.ResponseWriter, r *http.Request) {
	ctx := appengine.NewContext(r)
	project := appengine.AppID(ctx)

	transport := &oauth2.Transport{
		Source: google.AppEngineTokenSource(ctx, compute.ComputeScope),
		Base:   &urlfetch.Transport{Context: ctx},
	}
	client := &http.Client{Transport: transport}

	computeService, err := compute.New(client)
	if err != nil {
		log.Errorf(ctx, err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	disksService := compute.NewDisksService(computeService)
	snapshotsService := compute.NewSnapshotsService(computeService)

	dal, err := disksService.AggregatedList(project).Do()
	if err != nil {
		log.Errorf(ctx, err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	disks := make(map[uint64]*Disk)

	for z, v := range dal.Items {
		for _, v2 := range v.Disks {
			if kf, ok := v2.Labels[label]; ok {
				zone := strings.Split(z, "/")[1]
				disks[v2.Id] = new(Disk)
				disks[v2.Id].Id = v2.Id
				disks[v2.Id].Name = v2.Name
				disks[v2.Id].Status = v2.Status
				disks[v2.Id].Zone = zone
				disks[v2.Id].KeepFor, _ = strconv.Atoi(kf)
				if disks[v2.Id].Status == "READY" {
					n := fmt.Sprintf("%s-%d-%d", prefix, time.Now().UTC().Unix(), v2.Id)
					if _, err := disksService.CreateSnapshot(project, zone, v2.Name, &compute.Snapshot{
						Name: n,
					}).Do(); err != nil {
						log.Errorf(ctx, err.Error())
					} else {
						log.Infof(ctx, "Snapshot was created: %s (%s/%s)", n, zone, v2.Name)
					}
				}
			}
		}
	}

	sl, err := snapshotsService.List(project).Do()
	if err != nil {
		log.Errorf(ctx, err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	for _, v := range sl.Items {
		if v.Status == "READY" && strings.HasPrefix(v.Name, prefix) {
			id, _ := strconv.ParseUint(v.SourceDiskId, 0, 0)
			if t, err := time.Parse(time.RFC3339, v.CreationTimestamp); err == nil && disks[id] != nil && disks[id].KeepFor > 0 {
				if t.AddDate(0, 0, disks[id].KeepFor).Before(time.Now()) {
					if _, err := snapshotsService.Delete(project, v.Name).Do(); err != nil {
						log.Errorf(ctx, err.Error())
					} else {
						log.Infof(ctx, "Snapshot was deleted: %s (%s/%s) after %d days", v.Name, disks[id].Zone, disks[id].Name, disks[id].KeepFor)
					}
				}
			}
		}
	}
}