<?php
// Corpus seeded fault. Included by each app the way real site code is
// (WP mu-plugin, Joomla root defines.php, Drupal settings.php). It fatals ONLY on the new build (lab kit
// phosphor_lab_is_new(): PHP_VERSION contains "-phosphor", or PHP_VERSION_ID above /etc/phosphor-lab/base-version-id) and ONLY on
// the one request URI named in /etc/phosphor-lab/faults {"corpus-fatal-on-new": "<exact REQUEST_URI>"}. Every other
// URL, and every request on the old build, is untouched. PHP 5.6 → 8.x syntax.
require_once '/opt/phosphor-lab-kit/faults.php';

function phosphor_corpus_seed_check() {
    if (PHP_SAPI === 'cli' || !isset($_SERVER['REQUEST_URI']) || !phosphor_lab_is_new()) {
        return;
    }
    $f = phosphor_lab_faults();
    if (empty($f['corpus-fatal-on-new']) || $_SERVER['REQUEST_URI'] !== $f['corpus-fatal-on-new']) {
        return;
    }
    // what a real break looks like: a function the new runtime doesn't provide → "Call to undefined function"
    phosphor_corpus_seeded_function_missing_on_new();
}

phosphor_corpus_seed_check();
