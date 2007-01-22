import bongo.dragonfly

class HistoryHandler:
    def __init__(self):
        pass

    def do_GET(self, req):
        length = len(req.args)
        if length > 1024:
            raise HttpError(400, "Arguments too long")
        req.headers_out["Content-Length"] = str(length)
        req.headers_out["Cache-Control"] = "no-cache"
        req.content_type = "text/plain"
        req.write(req.args)
        return bongo.dragonfly.OK
