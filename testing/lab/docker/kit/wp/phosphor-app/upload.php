<?php
// first-party upload handler: uses $_FILES (usage evidence for the scan)
require '/usr/local/lib/phosphor-lab-kit/faults.php';
phosphor_lab_apply_faults();
if (!empty($_FILES['f']) && $_FILES['f']['error'] === UPLOAD_ERR_OK) {
    $dst = sys_get_temp_dir() . '/phosphor-upload-' . md5($_FILES['f']['name']);
    move_uploaded_file($_FILES['f']['tmp_name'], $dst);
    @unlink($dst);
    echo "stored\n";
} else {
    http_response_code(400);
    echo "no file\n";
}
