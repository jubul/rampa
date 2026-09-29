package kube

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"rampa.dev/pkg/protocol"
)

const (
	defaultLogContainer = "app"
	defaultLogFollow    = 60 * time.Second
	logBatchLines       = 64
	logBatchDelay       = 200 * time.Millisecond
	logLineLimit        = 1 << 20 // 1 MiB por línea, más que eso se corta
)

// LogSink recibe los chunks de log. Si devuelve error, se corta el streaming.
type LogSink func(chunk protocol.LogChunk) error

// Logs manda las últimas líneas de los pods del workload y, con Follow, sigue
// hasta DurationSeconds. Termina siempre con un chunk con EOF true (salvo que
// el sink falle). El CommandID de los chunks lo completa quien llama.
func (c *Client) Logs(ctx context.Context, cmd *protocol.LogsCommand, sink LogSink) error {
	if cmd == nil || cmd.App == "" || cmd.Namespace == "" {
		return errors.New("el comando logs necesita app y namespace")
	}
	if sink == nil {
		return errors.New("logs sin destino")
	}
	container := cmd.Container
	if container == "" {
		container = defaultLogContainer
	}

	pods, err := c.Typed.CoreV1().Pods(cmd.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{LabelName: cmd.App}).String(),
	})
	if err != nil {
		return fmt.Errorf("no pude listar los pods de %s: %w", cmd.App, err)
	}
	if len(pods.Items) == 0 {
		return sink(protocol.LogChunk{Container: container, EOF: true})
	}

	streamCtx := ctx
	if cmd.Follow {
		d := time.Duration(cmd.DurationSeconds) * time.Second
		if d <= 0 {
			d = defaultLogFollow
		}
		var cancel context.CancelFunc
		streamCtx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}

	opts := &corev1.PodLogOptions{Container: container, Follow: cmd.Follow}
	if cmd.TailLines > 0 {
		opts.TailLines = int64Ptr(int64(cmd.TailLines))
	}

	var (
		sinkMu   sync.Mutex
		sinkErr  error
		wg       sync.WaitGroup
		firstErr error
		errMu    sync.Mutex
	)
	send := func(chunk protocol.LogChunk) error {
		sinkMu.Lock()
		defer sinkMu.Unlock()
		if sinkErr != nil {
			return sinkErr
		}
		if err := sink(chunk); err != nil {
			sinkErr = err
			return err
		}
		return nil
	}
	recordErr := func(err error) {
		errMu.Lock()
		defer errMu.Unlock()
		if firstErr == nil {
			firstErr = err
		}
	}

	for i := range pods.Items {
		pod := pods.Items[i]
		if pod.Status.Phase == corev1.PodPending && !cmd.Follow {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.streamPodLogs(streamCtx, cmd.Namespace, pod.Name, container, opts, send); err != nil {
				recordErr(err)
			}
		}()
	}
	wg.Wait()

	if sinkErr != nil {
		return fmt.Errorf("se cortó el envío de logs: %w", sinkErr)
	}
	if err := send(protocol.LogChunk{Container: container, EOF: true}); err != nil {
		return err
	}
	// Si el contexto venció por Follow, no es un error: terminó el plazo pedido.
	if firstErr != nil && ctx.Err() == nil && !errors.Is(firstErr, context.DeadlineExceeded) {
		return firstErr
	}
	return nil
}

// streamPodLogs lee el log de un pod y lo manda en tandas de hasta logBatchLines
// líneas o cada logBatchDelay, lo que pase primero.
func (c *Client) streamPodLogs(ctx context.Context, namespace, pod, container string, opts *corev1.PodLogOptions, send func(protocol.LogChunk) error) error {
	stream, err := c.Typed.CoreV1().Pods(namespace).GetLogs(pod, opts).Stream(ctx)
	if err != nil {
		// Reportamos el motivo como una línea del propio pod para que el usuario lo vea.
		_ = send(protocol.LogChunk{Pod: pod, Container: container, Lines: []string{"[rampa] no pude leer los logs: " + err.Error()}})
		return fmt.Errorf("pod %s: %w", pod, err)
	}
	defer stream.Close()

	lines := make(chan string, logBatchLines)
	readErr := make(chan error, 1)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(stream)
		sc.Buffer(make([]byte, 64*1024), logLineLimit)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-ctx.Done():
				return
			}
		}
		if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
			readErr <- err
		}
	}()

	batch := make([]string, 0, logBatchLines)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		chunk := protocol.LogChunk{Pod: pod, Container: container, Lines: append([]string(nil), batch...)}
		batch = batch[:0]
		return send(chunk)
	}
	timer := time.NewTimer(logBatchDelay)
	timer.Stop()
	defer timer.Stop()

	for {
		select {
		case line, ok := <-lines:
			if !ok {
				if err := flush(); err != nil {
					return err
				}
				select {
				case err := <-readErr:
					if ctx.Err() != nil {
						return ctx.Err()
					}
					return fmt.Errorf("pod %s: %w", pod, err)
				default:
					return ctx.Err()
				}
			}
			if len(batch) == 0 {
				timer.Reset(logBatchDelay)
			}
			batch = append(batch, line)
			if len(batch) >= logBatchLines {
				timer.Stop()
				if err := flush(); err != nil {
					return err
				}
			}
		case <-timer.C:
			if err := flush(); err != nil {
				return err
			}
		case <-ctx.Done():
			// El stream se cierra por el defer; vaciamos lo que quedó y salimos.
			if err := flush(); err != nil {
				return err
			}
			return ctx.Err()
		}
	}
}
