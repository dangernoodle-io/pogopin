package cli

import (
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"dangernoodle.io/pogopin/internal/esp"
)

// gpioFlasherFactory creates the espflasher instance used by the gpio
// subcommands. Overridden in tests to avoid a real serial connection.
var gpioFlasherFactory esp.FlasherFactory = esp.DefaultFlasherFactory

var gpioCmd = &cobra.Command{
	Use:   "gpio",
	Short: "Read, drive, or sweep ESP GPIO pins over serial (no firmware required)",
	Long:  "GPIO operates directly against the ROM/stub bootloader's memory-mapped GPIO registers while the chip is in download mode, so pins can be probed or driven without flashing firmware. By default the chip is left in download mode when the command exits (no reset); pass --reset-after to reboot into the app.",
}

var gpioReadCmd = &cobra.Command{
	Use:   "read <pin>",
	Short: "Read the current level of a GPIO pin",
	Args:  cobra.ExactArgs(1),
	RunE:  runGPIORead,
}

var gpioSetCmd = &cobra.Command{
	Use:   "set <pin> <0|1>",
	Short: "Drive a GPIO pin high or low",
	Args:  cobra.ExactArgs(2),
	RunE:  runGPIOSet,
}

var gpioSweepCmd = &cobra.Command{
	Use:   "sweep [pins]",
	Short: "Sweep a set of candidate GPIO pins, dwelling on each; <pins> optional",
	Long:  "Sweep drives each pin in <pins> (a comma-separated list and/or ranges, e.g. \"4,16,17\" or \"0-21\") in turn, dwelling on each polarity, over a single connection. Useful for finding which pin drives an unlabeled LED without reflashing. <pins> optional; with no pins, sweeps every drivable (non-reserved) pin on the detected chip.",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runGPIOSweep,
}

var (
	gpioPort          string
	gpioBaud          int
	gpioResetMode     string
	gpioDwell         time.Duration
	gpioBoth          bool
	gpioIncludeRsv    bool
	gpioSetIncludeRsv bool
	gpioResetAfter    bool
)

func init() {
	gpioCmd.PersistentFlags().StringVar(&gpioPort, "port", "", "serial port (required)")
	gpioCmd.PersistentFlags().IntVar(&gpioBaud, "baud", 0, "baud rate (default 115200)")
	gpioCmd.PersistentFlags().StringVar(&gpioResetMode, "reset-mode", "", "reset mode: auto (default), default, usb_jtag, no_reset")
	gpioCmd.PersistentFlags().BoolVar(&gpioResetAfter, "reset-after", false, "reset the chip on exit, booting the app (default: leave the chip in download mode)")
	_ = gpioCmd.MarkPersistentFlagRequired("port")

	gpioSweepCmd.Flags().DurationVar(&gpioDwell, "dwell", 5*time.Second, "how long to hold each polarity before advancing")
	gpioSweepCmd.Flags().BoolVar(&gpioBoth, "both", true, "drive each pin both high and low")
	gpioSweepCmd.Flags().BoolVar(&gpioIncludeRsv, "include-reserved", false, "sweep pins normally skipped as reserved (flash/PSRAM, strapping, UART0, USB-JTAG, input-only)")

	gpioSetCmd.Flags().BoolVar(&gpioSetIncludeRsv, "include-reserved", false, "drive pins normally refused as reserved (flash/PSRAM, strapping, UART0, USB-JTAG)")

	gpioCmd.AddCommand(gpioReadCmd)
	gpioCmd.AddCommand(gpioSetCmd)
	gpioCmd.AddCommand(gpioSweepCmd)
}

// newGPIOStatusFunc returns a StatusFunc that prints each phase line to
// stderr as the operation progresses.
func newGPIOStatusFunc(cmd *cobra.Command) esp.StatusFunc {
	return func(phase string, current, total int) {
		if total > 0 {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s (%d/%d)\n", phase, current, total)
			return
		}
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s\n", phase)
	}
}

func runGPIORead(cmd *cobra.Command, args []string) error {
	pin, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Errorf("invalid pin %q: %w", args[0], err)
	}

	result, err := esp.ReadGPIO(gpioFlasherFactory, gpioPort, pin, gpioBaud, gpioResetMode, gpioResetAfter)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "GPIO %d: %s\n", result.Pin, levelString(result.Level))
	return nil
}

func runGPIOSet(cmd *cobra.Command, args []string) error {
	pin, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Errorf("invalid pin %q: %w", args[0], err)
	}

	level, err := parseLevel(args[1])
	if err != nil {
		return err
	}

	if err := esp.SetGPIO(gpioFlasherFactory, gpioPort, pin, level, gpioBaud, gpioResetMode, gpioResetAfter, gpioSetIncludeRsv); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "GPIO %d set %s\n", pin, levelString(level))
	return nil
}

func runGPIOSweep(cmd *cobra.Command, args []string) error {
	var pins []int
	if len(args) == 1 {
		parsed, err := parsePinList(args[0])
		if err != nil {
			return err
		}
		pins = parsed
	}

	opts := esp.GPIOSweepOpts{
		BothPolarities:  gpioBoth,
		Dwell:           gpioDwell,
		IncludeReserved: gpioIncludeRsv,
		Restore:         true,
	}

	result, err := esp.SweepGPIO(gpioFlasherFactory, gpioPort, pins, opts, gpioBaud, gpioResetMode, newGPIOStatusFunc(cmd), gpioResetAfter)
	if err != nil {
		return err
	}

	for _, outcome := range result.Pins {
		if outcome.Skipped {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "GPIO %d: skipped (%s)\n", outcome.Pin, outcome.Reason)
			continue
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "GPIO %d: swept\n", outcome.Pin)
	}
	return nil
}

func levelString(level bool) string {
	if level {
		return "high"
	}
	return "low"
}

func parseLevel(s string) (bool, error) {
	switch s {
	case "1":
		return true, nil
	case "0":
		return false, nil
	default:
		return false, fmt.Errorf("invalid level %q: expected 0 or 1", s)
	}
}

// parsePinList delegates to esp.ParsePinList (also used by the
// esp_gpio_sweep MCP tool) so pin-list syntax and the max-pins cap stay
// identical across both surfaces.
func parsePinList(s string) ([]int, error) {
	return esp.ParsePinList(s)
}
