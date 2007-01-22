from mod_python import apache, util
from ApacheLogHandler import ApacheLogHandler, RequestLogProxy

from HttpError import HttpError

import bongo.dragonfly

from bongo.dragonfly.HistoryHandler import HistoryHandler

# Add a handler so messages from python's logging module go to apache.
# We don't use that module in the Bongo server proper, because we need
# to log to apache requests directly
import logging
logging.getLogger('').addHandler(ApacheLogHandler())
logging.root.setLevel(logging.DEBUG)

def handler(req):
    req.log = RequestLogProxy(req)

    try:
        handler = HistoryHandler()
        mname = "do_" + req.method
        
        if not hasattr(handler, mname):
            return bongo.dragonfly.HTTP_NOT_IMPLEMENTED

        method = getattr(handler, mname)
        ret = method(req)

        if ret is None:
            self.send_response(bongo.dragonfly.HTTP_OK)
        return ret
    except HttpError, e:
        return e.code
