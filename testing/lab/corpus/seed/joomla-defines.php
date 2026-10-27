<?php
// Joomla 3 index.php includes <root>/defines.php when present; without _JDEFINES it then continues normally.
// Corpus seed hook (phosphor lab) — see /corpus-seed/phosphor_corpus_seed.php
if (is_readable('/corpus-seed/phosphor_corpus_seed.php')) {
    include_once '/corpus-seed/phosphor_corpus_seed.php';
}
