from bongo.commonweb.ApacheLogHandler import ApacheLogHandler, RequestLogProxy
from SundialPath import SundialPath
from bongo.commonweb.HttpError import HttpError

import Auth
import bongo.commonweb

# Add a handler so messages from python's logging module go to apache.
# We don't use that module in the Bongo server proper, because we need
# to log to apache requests directly
import logging
logging.getLogger('').addHandler(ApacheLogHandler())
logging.root.setLevel(logging.DEBUG)

def handler(req):
    req.log = RequestLogProxy(req)
    
    req.log.debug("Request: %s", req)

    try:
        rp = SundialPath(req)
        handler = rp.GetHandler(req, rp)
        
        
#        if handler.NeedsAuth(rp):
#            if req.user is None:
#                self.req.headers_out["WWW-Authenticate"] = 'Basic realm="Bongo"'
#                self.send_error(bongo.commonweb.HTTP_UNAUTHORIZED)
#                return bongo.commonweb.HTTP_UNAUTHORIZED
#            else:
#                auth = bongo.sundial.Auth.authenhandler(req)

        req.log.debug("request for %s (handled by %s)", req.path_info, handler)

        mname = "do_" + req.method

        if not hasattr(handler, mname):
            req.log.debug("%s has no %s", handler, mname)
            return bongo.commonweb.HTTP_NOT_FOUND

        method = getattr(handler, mname)
        
        ret = method(req, rp)

        if ret is None:
            req.log.error("Handler %s returned invalid value: None" % handler)
            return bongo.commonweb.OK

        return ret
    except HttpError, e:
        req.log.debug(str(e), exc_info=1)
        return e.code
