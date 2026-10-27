<?php
// first-party code that runs on 7.4 and 8.2 but uses constructs DEPRECATED in 8.2 (not removed): PHPCompatibility at
// testVersion 8.2- reports warnings only, no errors (fixture variant APP=deprec82).
function phosphor_greeting($name) {
    $latin = utf8_decode($name);                 // utf8_decode(): deprecated since 8.2
    $label = "Hello ${name}";                    // "${var}" string interpolation: deprecated since 8.2
    return utf8_encode($latin) . ' / ' . $label; // utf8_encode(): deprecated since 8.2
}
echo phosphor_greeting(isset($_GET['n']) ? (string) $_GET['n'] : 'phosphor'), "\n";
