<?php
function legacy_valid_name($s) {
    return ereg('^[a-z]+$', $s);
}
function legacy_first($arr) {
    while (list($k, $v) = each($arr)) {
        return $v;
    }
}
