package agoreum

// Waiting for an order to move, without guessing at its status word.
//
// An order moves along one path, pending_payment, funded, in_progress,
// delivered, completed, and can leave it: disputed, cancelled, refunded or
// expired. A target counts as reached once the order is at it or further along
// the path, so a step that passed between two polls is not missed. An order
// that leaves the path short of the target returns *OrderLeftPath at once. A
// dispute counts as funded, since only a funded escrow can be disputed.

import (
	"context"
	"fmt"
	"time"
)

var (
	orderPath    = []string{"pending_payment", "funded", "in_progress", "delivered", "completed"}
	orderOffPath = map[string]bool{"disputed": true, "cancelled": true, "refunded": true, "expired": true}
)

// OrderLeftPath is returned when the order left the normal path before
// reaching the status waited for.
type OrderLeftPath struct{ OrderID, Status, Target string }

func (e *OrderLeftPath) Error() string {
	return fmt.Sprintf("agoreum: order %s is %s, so it will not reach %s", e.OrderID, e.Status, e.Target)
}

// OrderWaitTimeout is returned when the order had not reached the status
// waited for when the time ran out.
type OrderWaitTimeout struct {
	OrderID, Status, Target string
	Timeout                 time.Duration
}

func (e *OrderWaitTimeout) Error() string {
	return fmt.Sprintf("agoreum: order %s was still %s after %s, waiting for %s", e.OrderID, e.Status, e.Timeout, e.Target)
}

func pathIndex(status string) int {
	for i, s := range orderPath {
		if s == status {
			return i
		}
	}
	return -1
}

// Reached reports whether status satisfies target, and returns *OrderLeftPath
// when the order can no longer get there.
func Reached(orderID, status, target string) (bool, error) {
	if status == target {
		return true, nil
	}
	if si, ti := pathIndex(status), pathIndex(target); si >= 0 && ti >= 0 {
		return si >= ti, nil
	}
	if status == "disputed" && target == "funded" {
		return true, nil
	}
	if orderOffPath[status] {
		return false, &OrderLeftPath{OrderID: orderID, Status: status, Target: target}
	}
	return false, nil
}

// WaitForStatus polls until the order reaches target or anywhere past it on
// the normal path, and returns it. WaitForStatus(ctx, id, "funded", 0, 0)
// after Fund waits for Agoreum's indexer to see the escrow. Zero timeout and
// interval mean 5 minutes and 5 seconds. It returns *OrderLeftPath as soon as
// the order is disputed, cancelled, refunded or expired short of the target,
// and *OrderWaitTimeout when the time runs out.
func (o *Orders) WaitForStatus(ctx context.Context, orderID, target string, timeout, interval time.Duration) (Order, error) {
	if pathIndex(target) < 0 && !orderOffPath[target] {
		return Order{}, fmt.Errorf("agoreum: unknown order status %q", target)
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		order, err := o.Get(ctx, orderID)
		if err != nil {
			return Order{}, err
		}
		ok, err := Reached(orderID, order.Status, target)
		if err != nil {
			return order, err
		}
		if ok {
			return order, nil
		}
		if time.Now().Add(interval).After(deadline) {
			return order, &OrderWaitTimeout{OrderID: orderID, Status: order.Status, Target: target, Timeout: timeout}
		}
		select {
		case <-ctx.Done():
			return order, ctx.Err()
		case <-time.After(interval):
		}
	}
}
