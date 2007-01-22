import bongo.dragonfly

def redirect(req, location, permanent=0, text=None):
    """
    A convenience function to provide redirection
    """

    if req.sent_bodyct:
        raise IOError, "Cannot redirect after headers have already been sent."

    req.headers_out["Location"] = location
    if permanent:
        req.status = bongo.dragonfly.HTTP_MOVED_PERMANENTLY
    else:
        req.status = bongo.dragonfly.HTTP_MOVED_TEMPORARILY

    if text is None:
        req.write('<p>The document has moved'
                  ' <a href="%s">here</a></p>\n'
                  % location)
    else:
        req.write(text)

    return req.status
