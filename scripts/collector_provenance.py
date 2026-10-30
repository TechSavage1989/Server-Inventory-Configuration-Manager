"""Reverse only explicitly reviewed collector edits; never bless whole-file drift."""
import hashlib


def sha256(data):
    return hashlib.sha256(data).hexdigest()


def restore_reviewed_regions(current, archived, regions):
    """Validate every unchanged byte and every changed span before restoring it.

    Offsets refer to version/HTML-normalized archived bytes. The current side of
    each region is independently length/hash pinned, so deleting or broadening a
    marker cannot silently enlarge the allowed change. Original archive bytes are
    authenticated by the caller and remain authoritative, not current snapshots.
    """
    before_pos = 0
    after_pos = 0
    for index, region in enumerate(regions):
        start, end = region['before_start'], region['before_end']
        length = region['after_length']
        if not (isinstance(start, int) and isinstance(end, int) and
                isinstance(length, int) and 0 <= before_pos <= start <= end <= len(archived)
                and length >= 0):
            raise ValueError('Invalid or overlapping reviewed region %d' % index)
        if not region.get('reason'):
            raise ValueError('Reviewed region must document its purpose')
        unchanged = archived[before_pos:start]
        if current[after_pos:after_pos + len(unchanged)] != unchanged:
            raise ValueError('Unapproved collector change before reviewed region %d' % index)
        after_pos += len(unchanged)
        changed = current[after_pos:after_pos + length]
        if len(changed) != length or sha256(changed) != region['after_sha256']:
            raise ValueError('Approved collector span changed at region %d' % index)
        if sha256(archived[start:end]) != region['before_sha256']:
            raise ValueError('Reviewed archived span mismatch at region %d' % index)
        before_pos = end
        after_pos += length
    if current[after_pos:] != archived[before_pos:]:
        raise ValueError('Unapproved collector change after reviewed regions')
    return archived
