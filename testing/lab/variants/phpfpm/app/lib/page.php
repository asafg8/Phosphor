<?php
function phosphor_page($name, $n) {
    $items = array();
    for ($i = 0; $i < 20; $i++) {
        $items[] = sprintf('%s-%d-%d', $name, $n, $i);
    }
    echo "<html><body><h1>phosphor lab " . htmlspecialchars($name) . "</h1><ul>";
    foreach ($items as $it) {
        echo '<li>' . htmlspecialchars($it) . '</li>';
    }
    echo "</ul></body></html>\n";
}
