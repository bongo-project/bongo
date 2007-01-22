import logging

class HttpError(Exception):
    log = logging.getLogger("Dragonfly")

    def __init__(self, code, message=None):
        self.log.debug("%d %s" % (code, message))
        self.log.info("%d %s" % (code, message))
        self.log.warning("%d %s" % (code, message))
        self.log.error("%d %s" % (code, message))
        self.log.critical("%d %s" % (code, message))
        self.code = code
        self.message = message

    def __repr__(self):
        return repr((self.code, self.message))

    def __str__(self):
        return "%d %s" % (self.code, self.message)
