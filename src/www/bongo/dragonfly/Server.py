from mod_python import apache, util
from bongo.commonweb.ApacheLogHandler import ApacheLogHandler, RequestLogProxy
from ResourcePath import ResourcePath
from HttpError import HttpError

import Auth
import bongo.commonweb.BongoFieldStorage
import bongo.commonweb

# Add a handler so messages from python's logging module go to apache.
# We don't use that module in the Bongo server proper, because we need
# to log to apache requests directly
import logging
logging.getLogger('').addHandler(ApacheLogHandler())
logging.root.setLevel(logging.DEBUG)

def handler(req):
    req.log = RequestLogProxy(req)

    # if the req is multipart/form-data, decode its contents now, so
    # we can use the authentication field in the auth handler.
    req.form = None
    reqtype = req.headers_in.get("Content-Type", None)
    if reqtype and (reqtype.startswith("application/x-www-form-urlencoded")
                    or reqtype.startswith("multipart/form-data")):
        req.form = BongoFieldStorage.get_fieldstorage(req)

    auth = Auth.authenhandler(req)
    if auth != bongo.commonweb.HTTP_OK:
        return auth

    try:
        rp = ResourcePath(req)
        handler = rp.GetHandler()
        
        req.log.debug("request for %s (handled by %s)", req.path_info, handler)
        command = req.headers_in.get("X-Bongo-HttpMethod", req.method)

        mname = "do_" + command
        if not hasattr(handler, mname):
            req.log.debug("%s has no %s", handler, mname)
            return bongo.commonweb.HTTP_NOT_IMPLEMENTED

        method = getattr(handler, mname)
        ret = method(req, rp)

        if ret is None:
            req.log.error("Handler %s returned invalid value: None" % handler)
            return bongo.commonweb.OK
        return ret
    except HttpError, e:
        return e.code
