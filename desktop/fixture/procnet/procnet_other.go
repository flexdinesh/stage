//go:build !linux && !darwin

package procnet

func listeningTCP(int) ([]string, error) { return nil, ErrUnsupported }
