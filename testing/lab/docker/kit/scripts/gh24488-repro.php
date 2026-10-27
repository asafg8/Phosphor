<?php
// php-src gh-24488 reproducer. Stock 8.4.27 / 8.5.12:
// "zend_mm_heap corrupted" (non-zero exit); fixed or unaffected builds print "done". Run: <php> -n gh24488-repro.php
for ($i = 0; $i < 2; $i++) {
    $xml = new SimpleXMLElement('<a><b/></a>');
    get_object_vars($xml);
    $holder = new stdClass; $holder->xml = $xml; $holder->self = $holder;
    unset($xml, $holder);
    gc_collect_cycles();
    $junk[] = str_repeat('x', 100);
}
echo "done\n";
