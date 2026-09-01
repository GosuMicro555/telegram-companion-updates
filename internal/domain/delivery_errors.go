package domain

import "errors"

type DeliveryFailureClass string

const (
	DeliveryFailurePermanent DeliveryFailureClass = "permanent"
	DeliveryFailureTransient DeliveryFailureClass = "transient"
)

type classifiedDeliveryFailure struct {
	class DeliveryFailureClass
	err   error
}

type privateMessageClosedError struct {
	err error
}

func (e *classifiedDeliveryFailure) Error() string                              { return e.err.Error() }
func (e *classifiedDeliveryFailure) Unwrap() error                              { return e.err }
func (e *classifiedDeliveryFailure) DeliveryFailureClass() DeliveryFailureClass { return e.class }

func (e *privateMessageClosedError) Error() string { return e.err.Error() }
func (e *privateMessageClosedError) Unwrap() error { return e.err }

func PermanentDeliveryFailure(err error) error {
	return classifyDeliveryFailure(err, DeliveryFailurePermanent)
}
func TransientDeliveryFailure(err error) error {
	return classifyDeliveryFailure(err, DeliveryFailureTransient)
}

func PrivateMessageClosed(err error) error {
	if err == nil {
		return nil
	}
	return &privateMessageClosedError{err: err}
}

func classifyDeliveryFailure(err error, class DeliveryFailureClass) error {
	if err == nil {
		return nil
	}
	return &classifiedDeliveryFailure{class: class, err: err}
}

func IsPermanentDeliveryFailure(err error) bool {
	return deliveryFailureClass(err) == DeliveryFailurePermanent
}
func IsTransientDeliveryFailure(err error) bool {
	return deliveryFailureClass(err) == DeliveryFailureTransient
}

func IsPrivateMessageClosed(err error) bool {
	var closed *privateMessageClosedError
	return errors.As(err, &closed)
}

func deliveryFailureClass(err error) DeliveryFailureClass {
	var classified interface{ DeliveryFailureClass() DeliveryFailureClass }
	if errors.As(err, &classified) {
		return classified.DeliveryFailureClass()
	}
	return ""
}
