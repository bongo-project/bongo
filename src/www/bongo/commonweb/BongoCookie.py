import time

# Apache implementation follows
def add_cookie_apache(req, cookie, value=None, **kwargs):
    return ApacheCookie.add_cookie(req, cookie, value=value, **kwargs)

def get_cookies_apache(req, parser=None, **kwargs):
    return ApacheCookie.get_cookies(req, ApacheCookie.Cookie, **kwargs)

# Python implementation follows
def add_cookie_python(req, cookie, value=None, **kwargs):
    req.cookies_out[cookie] = value
    if kwargs.has_key("expires"):
        t = kwargs["expires"]
        kwargs["expires"] = int(t - time.time())
    req.cookies_out[cookie].update(kwargs)

def get_cookies_python(req, parser=None, **kwargs):
    return req.cookies_in

try:
    import _apache
    from mod_python import Cookie as ApacheCookie
    (add_cookie, get_cookies) = (add_cookie_apache, get_cookies_apache)
except ImportError:
    import Cookie as PythonCookie
    (add_cookie, get_cookies) = (add_cookie_python, get_cookies_python)
