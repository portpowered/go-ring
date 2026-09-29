package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

func replayVideoCommand(args []string, out io.Writer) error {
	if len(args) < 1 {
		return commandError("replay-video requires an RTP recording")
	}

	flags := flag.NewFlagSet("replay-video", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	outputPath := flags.String("output", "", "Annex B H264 output")

	err := flags.Parse(args[1:])
	if err != nil {
		return wrapCommandError("parse replay-video flags", err)
	}

	if *outputPath == "" || flags.NArg() != 0 {
		return commandError("usage: replay-video <recording> --output file.h264")
	}

	input, err := os.Open(args[0])
	if err != nil {
		return wrapCommandError("open RTP recording", err)
	}

	defer func() { _ = input.Close() }()

	output, err := os.OpenFile(*outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, privateFileMode)
	if err != nil {
		return wrapCommandError("create H264 output file", err)
	}

	defer func() { _ = output.Close() }()

	err = output.Chmod(privateFileMode)
	if err != nil {
		_ = output.Close()
		_ = os.Remove(*outputPath)

		return wrapCommandError("set H264 output permissions", err)
	}

	err = restrictTokenFile(*outputPath)
	if err != nil {
		_ = output.Close()
		_ = os.Remove(*outputPath)

		return wrapCommandError("set H264 output permissions", err)
	}

	writer := newH264FrameWriter(output)

	err = replayRTP(input, writer.WriteRTP)
	if err != nil {
		return wrapCommandError("replay RTP recording", err)
	}

	err = writer.Close()
	if err != nil {
		return wrapCommandError("finish H264 output", err)
	}

	_, err = fmt.Fprintf(out, "Saved %s (H264 video from recorded RTP)\n", *outputPath)
	if err != nil {
		return wrapCommandError("print replay result", err)
	}

	return nil
}
