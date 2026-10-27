<?php
// phosphor-lab fault reader. Faults live in /etc/phosphor-lab/faults (JSON) and only bite on the NEW
// build (the swap target or the build verify checks): PHP_VERSION carries our build marker ("-phosphor1", "+phosphor1",
// "~phosphor1": upstream and distro-source builds), or (8.x) PHP_VERSION_ID is above the id recorded at image build.
// Works on PHP 5.6 → 8.x (no ??, no str_contains).

function phosphor_lab_is_new() {
    if (preg_match('/[-+~.]phosphor[0-9]/', PHP_VERSION)) {
        return true;
    }
    $f = '/etc/phosphor-lab/base-version-id';
    if (is_readable($f)) {
        $base = (int) trim(file_get_contents($f));
        if ($base > 0 && PHP_VERSION_ID > $base) {
            return true;
        }
    }
    return false;
}

function phosphor_lab_faults() {
    $f = '/etc/phosphor-lab/faults';
    if (!is_readable($f)) {
        return array();
    }
    $j = json_decode(file_get_contents($f), true);
    return is_array($j) ? $j : array();
}

// phosphor_lab_apply_faults runs at the top of every app page (not the probe route).
function phosphor_lab_apply_faults() {
    $faults = phosphor_lab_faults();
    if (isset($faults['flaky-both'])) {
        // flaky-both: one URL (path=, default "/?p=2") fails at rate r on the OLD and NEW build alike
        $fb = is_array($faults['flaky-both']) ? $faults['flaky-both'] : array();
        $path = isset($fb['path']) ? (string) $fb['path'] : '/?p=2';
        $rate = isset($fb['rate']) ? (float) $fb['rate'] : 0.5;
        if (isset($_SERVER['REQUEST_URI']) && $_SERVER['REQUEST_URI'] === $path && mt_rand() / mt_getrandmax() < $rate) {
            header('HTTP/1.1 500 Internal Server Error', true, 500);
            echo "phosphor-lab flaky-both\n";
            exit;
        }
    }
    if (!phosphor_lab_is_new()) {
        return;
    }
    if (isset($faults['slow-on-new'])) {
        // latency on the new build; default 150 ms (> +50 ms and > 1.5x a few-ms baseline)
        $ms = is_array($faults['slow-on-new']) && isset($faults['slow-on-new']['ms']) ? (int) $faults['slow-on-new']['ms'] : 150;
        usleep(max(0, $ms) * 1000);
    }
    if (isset($faults['deprecation-on-new']) && $faults['deprecation-on-new']) {
        // a deprecation, no fatal. E_USER_DEPRECATED is logged under php.ini-production's error_reporting
        // (which masks plain E_DEPRECATED); the log line reads "PHP Deprecated: …".
        trigger_error('phosphor-lab deprecation-on-new: phosphor_lab_legacy_call() is deprecated', E_USER_DEPRECATED);
    }
    if (isset($faults['fatal-on-new']) && $faults['fatal-on-new']) {
        phosphor_lab_fatal_on_new_undefined_function();
    }
    if (isset($faults['5xx-spike'])) {
        $rate = isset($faults['5xx-spike']['rate']) ? (float) $faults['5xx-spike']['rate'] : 1.0;
        if (mt_rand() / mt_getrandmax() < $rate) {
            header('HTTP/1.1 500 Internal Server Error', true, 500);
            echo "phosphor-lab 5xx-spike\n";
            exit;
        }
    }
}
