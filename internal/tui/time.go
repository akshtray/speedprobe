package tui

import "time"

const flashDuration = 3 * time.Second

func secondsToDuration(s int) time.Duration { return time.Duration(s) * time.Second }
