package maintenance_test

import (
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/maintenance"
)

func TestCoordinatorSerializesPublicationAndMaintenance(t *testing.T) {
	coordinator := maintenance.NewCoordinator()
	releasePublication := coordinator.AcquirePublication()
	acquired := make(chan func(), 1)
	go func() {
		acquired <- coordinator.AcquireMaintenance()
	}()
	select {
	case release := <-acquired:
		release()
		t.Fatal("publication 活跃时维护操作取得了排他锁")
	case <-time.After(30 * time.Millisecond):
	}
	releasePublication()
	select {
	case release := <-acquired:
		release()
	case <-time.After(time.Second):
		t.Fatal("publication 结束后维护操作仍未取得锁")
	}

	releaseMaintenance := coordinator.AcquireMaintenance()
	publicationAcquired := make(chan func(), 1)
	go func() {
		publicationAcquired <- coordinator.AcquirePublication()
	}()
	select {
	case release := <-publicationAcquired:
		release()
		t.Fatal("维护操作活跃时 publication 取得了读锁")
	case <-time.After(30 * time.Millisecond):
	}
	releaseMaintenance()
	select {
	case release := <-publicationAcquired:
		release()
	case <-time.After(time.Second):
		t.Fatal("维护操作结束后 publication 仍未取得锁")
	}
}
