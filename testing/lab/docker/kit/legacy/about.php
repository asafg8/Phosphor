<?
// short open tag (runtime-risk pack: short_open_tag)
require '/usr/local/lib/phosphor-lab-kit/faults.php';
phosphor_lab_apply_faults();
include __DIR__ . '/lib/page.php';
phosphor_page('about', 1);
