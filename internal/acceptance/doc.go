// Package acceptance holds the tier-2 checks that keep the verification tiers
// honest: every unit acceptance case a slice lists under docs/acceptance is
// named by a test, no test names a case no list has, and no test under
// internal/ waits on the wall clock.
package acceptance
