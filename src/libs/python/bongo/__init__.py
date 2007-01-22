from BongoError import BongoError

# I'm having very strange issues trying to import bongo.dragonfly from
# our web service.  When running under Apache, bongo.dragonfly can't be
# found, even though it's in sys.path and bongo.__path__.  I don't know
# of any way to fix it, except to try to import it here.

# If you'd like to fix it, get Bongo running under Apache, then remove
# the block below.  I'd love a fix that doesn't pull in dragonfly just
# because bongo was imported.
try:
    import dragonfly
    import hawkeye
except:
    pass
