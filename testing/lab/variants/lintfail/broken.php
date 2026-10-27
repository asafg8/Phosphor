<?php
// first-party file that does not compile on ANY PHP version (fixture variant APP=lintfail):
// `php -l` fails on the live 7.4 binary (pre-existing → review) and on 8.2 (→ will-break).
function phosphor_report_total(array $rows {
    return array_sum($rows);
}
