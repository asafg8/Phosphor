<?php
// phosphor-lab app kit router: /_phosphor_lab/{version,health,gd,upload,legacy-fatal,ini,progress,iniset,hog,oom74,sleep,segv,
// mapped,redis}. Mapped by AliasMatch (apache)
// or a SCRIPT_FILENAME location (nginx) so it is not a code root.
require __DIR__ . '/faults.php';

$path = parse_url(isset($_SERVER['REQUEST_URI']) ? $_SERVER['REQUEST_URI'] : '/', PHP_URL_PATH);
$route = preg_replace('#^(/(asg|u2204))?/_phosphor_lab/?#', '', $path);
header('Content-Type: text/plain');

switch ($route) {
case 'version':
    echo PHP_VERSION;
    break;
case 'health':
    $faults = phosphor_lab_faults();
    if (phosphor_lab_is_new() && !empty($faults['probe-fail-on-new'])) {
        header('HTTP/1.1 503 Service Unavailable', true, 503);
        echo "unhealthy\n";
        break;
    }
    echo "ok\n";
    break;
case 'gd':
    $im = imagecreatetruecolor(2, 2);
    echo $im ? "gd ok\n" : "gd fail\n";
    break;
case 'upload':
    phosphor_lab_apply_faults();
    if (empty($_FILES)) {
        header('HTTP/1.1 500 Internal Server Error', true, 500);
        echo "no upload\n";
        break;
    }
    echo "upload ok\n";
    break;
case 'legacy-fatal':
    phosphor_lab_legacy_fatal_pre_existing();
    break;
case 'ini':
    $k = isset($_GET['k']) ? $_GET['k'] : '';
    $v = ini_get($k);
    echo $v === false ? "unset\n" : $v . "\n";
    break;
case 'progress':
    // loadgen polls this; 500 once session upload progress is off (mitigation applied).
    if (!ini_get('session.upload_progress.enabled')) {
        header('HTTP/1.1 500 Internal Server Error', true, 500);
        echo "upload progress disabled\n";
        break;
    }
    echo "upload progress enabled\n";
    break;
case 'iniset':
    // app-level ini_set must not undo php_admin_* mitigations.
    @ini_set('memory_limit', '128M');
    @ini_set('session.upload_progress.enabled', '1');
    echo 'memory_limit=' . ini_get('memory_limit') . "\n";
    echo 'session.upload_progress.enabled=' . ini_get('session.upload_progress.enabled') . "\n";
    break;
case 'hog':
    // memory-cap test page: allocate ?mb=N MiB and hold it (memory_limit=-1 + MemoryMax → OOM kill of this worker).
    $mb = isset($_GET['mb']) ? max(1, (int) $_GET['mb']) : 64;
    $hold = array();
    for ($i = 0; $i < $mb; $i++) {
        $hold[] = str_repeat(chr(65 + $i % 26), 1048576);
    }
    echo 'allocated ' . $mb . 'M peak ' . memory_get_peak_usage(true) . "\n";
    break;
case 'oom74':
    // allocate until memory_limit; the original 7.4 logs "Allowed memory
    // size" + runs the shutdown handler, the hard-limit +phosphor1 build kills the worker (no response, nothing logged).
    register_shutdown_function(function () { error_log('PHOSPHOR shutdown handler ran'); });
    echo "start\n";
    $a = array();
    while (true) {
        $a[] = str_repeat('x', 1 << 16);
    }
    break;
case 'sleep':
    // flood helper: hold a worker ?s=N seconds (pm.max_children saturation)
    sleep(min(120, max(0, isset($_GET['s']) ? (int) $_GET['s'] : 10)));
    echo "slept\n";
    break;
case 'segv':
    // probe-path segv: SIGSEGV this worker, only on the new build (old build answers 200)
    if (phosphor_lab_is_new()) {
        if (function_exists('posix_kill')) {
            posix_kill(getmypid(), 11);
        }
        @exec('kill -SEGV ' . getmypid());
    }
    echo "ok\n";
    break;
case 'redis':
    // packaged/PECL redis extension in use (PHPREDIS=1 variants): 200 when the class loads and
    // a client object can be built; no redis server is needed
    if (!class_exists('Redis')) {
        header('HTTP/1.1 500 Internal Server Error', true, 500);
        echo "no Redis class\n";
        break;
    }
    $r = new Redis();
    echo 'redis ' . phpversion('redis') . "\n";
    break;
case 'mapped':
    // verify: the PHP binary/library THIS process has mapped (mod_php: libphp*.so, else the executable),
    // its mapped inode vs the inode now on disk, and GNU build-ids: build_id of the mapped image (memory, or the file
    // when it is still the mapped inode) and disk_build_id of the file now at that path.
    foreach (phosphor_lab_mapped() as $k => $v) {
        echo $k . '=' . $v . "\n";
    }
    break;
default:
    header('HTTP/1.1 404 Not Found', true, 404);
    echo "unknown\n";
}

function phosphor_lab_mapped() {
    $maps = @file('/proc/self/maps', FILE_IGNORE_NEW_LINES);
    $exe = @readlink('/proc/self/exe');
    $want = null;
    foreach ((array) $maps as $l) {
        if (preg_match('#\s(/\S*libphp[^/\s]*\.so)( \(deleted\))?$#', $l)) { $want = 'libphp'; break; }
    }
    $out = array('sapi' => PHP_SAPI, 'version' => PHP_VERSION);
    foreach ((array) $maps as $l) {
        $f = preg_split('/\s+/', $l, 6);
        if (count($f) < 6) { continue; }
        $path = $f[5];
        $deleted = false;
        if (substr($path, -10) === ' (deleted)') { $path = substr($path, 0, -10); $deleted = true; }
        $hit = $want === 'libphp' ? (strpos(basename($path), 'libphp') === 0) : ($path === preg_replace('# \(deleted\)$#', '', (string) $exe));
        if (!$hit || hexdec($f[2]) !== 0) { continue; }
        list($start) = explode('-', $f[0]);
        $st = @stat($path);
        $out['path'] = $path;
        $out['deleted'] = $deleted ? '1' : '0';
        $out['inode'] = $f[4];
        $out['disk_inode'] = $st ? (string) $st['ino'] : 'none';
        $disk = $st ? phosphor_lab_build_id($path, null) : 'none';
        $mem = phosphor_lab_build_id('/proc/self/mem', hexdec($start));
        // build_id = the mapped image's; from memory when readable, else from the file if it is still the mapped one
        $out['build_id'] = $mem !== 'unreadable' ? $mem : ((!$deleted && $st && (string) $st['ino'] === $f[4]) ? $disk : 'unknown');
        $out['build_id_source'] = $mem !== 'unreadable' ? 'memory' : ($out['build_id'] === 'unknown' ? 'none' : 'disk');
        $out['disk_build_id'] = $disk;
        return $out;
    }
    $out['path'] = 'unknown';
    return $out;
}

// GNU build-id of an ELF64-LE image: in memory at $base (/proc/self/mem; fpm/apache workers are usually not
// dumpable, so this often fails) or from a file on disk ($base = null).
function phosphor_lab_build_id($src, $base) {
    $m = @fopen($src, 'rb');
    if (!$m) { return 'unreadable'; }
    $rd = function ($addr, $n) use ($m) { if ($n <= 0 || @fseek($m, $addr) !== 0) { return ''; } return (string) @fread($m, $n); };
    $b = $base === null ? 0 : $base;
    $eh = $rd($b, 64);
    if (strlen($eh) < 64 || substr($eh, 0, 4) !== "\x7fELF" || ord($eh[4]) !== 2) { fclose($m); return $eh === '' ? 'unreadable' : 'not-elf64'; }
    $h = unpack('Pphoff', substr($eh, 32, 8));
    $p = unpack('vphentsize/vphnum', substr($eh, 54, 4));
    $phs = $rd($b + $h['phoff'], $p['phentsize'] * $p['phnum']);
    $load0 = null; $notes = array();
    for ($i = 0; $i < $p['phnum']; $i++) {
        $t = unpack('Vtype/Vflags/Poffset/Pvaddr/Ppaddr/Pfilesz', substr($phs, $i * $p['phentsize'], 40));
        if ($t['type'] === 1 && $load0 === null) { $load0 = $t['vaddr']; }
        if ($t['type'] === 4) { $notes[] = $t; }
    }
    foreach ($notes as $n) {
        $at = $base === null ? $n['offset'] : $base - ($load0 === null ? 0 : $load0) + $n['vaddr'];
        $buf = $rd($at, $n['filesz']);
        $o = 0;
        while ($o + 12 <= strlen($buf)) {
            $hd = unpack('Vnamesz/Vdescsz/Vtype', substr($buf, $o, 12));
            $name = substr($buf, $o + 12, $hd['namesz']);
            $doff = $o + 12 + (($hd['namesz'] + 3) & ~3);
            if ($hd['type'] === 3 && rtrim($name, "\0") === 'GNU') { fclose($m); return bin2hex(substr($buf, $doff, $hd['descsz'])); }
            $o = $doff + (($hd['descsz'] + 3) & ~3);
        }
    }
    fclose($m);
    return 'none';
}
