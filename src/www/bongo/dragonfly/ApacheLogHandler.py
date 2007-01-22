from mod_python import apache
import sys, logging
import cStringIO as StringIO

class RequestLogProxy:
    def __init__(self, req):
        self.req = req

    def _format_exception(self, ei):
        """
        Format and return the specified exception information as a string.
        
        This default implementation just uses
        traceback.print_exception()
        """
        import traceback
        sio = StringIO.StringIO()
        traceback.print_exception(ei[0], ei[1], ei[2], None, sio)
        s = sio.getvalue()
        sio.close()
        if s[-1] == "\n":
            s = s[:-1]
            return s

    def _format_message(self, message, *args, **kwargs):
        s = message % args

        if kwargs.get("exc_info"):
            if s[-1] != "\n":
                s = s + "\n"
            s = s + self._format_exception(sys.exc_info())

        return s

    def debug(self, message, *args, **kwargs):
        msg = self._format_message(message, *args, **kwargs)
        self.req.log_error(msg, apache.APLOG_ERR)

    def info(self, message, *args, **kwargs):
        msg = self._format_message(message, *args, **kwargs)
        self.req.log_error(msg, apache.APLOG_INFO)

    def warning(self, message, *args, **kwargs):
        msg = self._format_message(message, *args, **kwargs)
        self.req.log_error(msg, apache.APLOG_WARNING)

    def error(self, message, *args, **kwargs):
        msg = self._format_message(message, *args, **kwargs)
        self.req.log_error(msg, apache.APLOG_ERR)

    def critical(self, message, *args, **kwargs):
        msg = self._format_message(message, *args, **kwargs)
        self.req.log_error(msg, apache.APLOG_CRIT)

class ApacheLogHandler(logging.Handler):
    typemap = {
        logging.CRITICAL : apache.APLOG_CRIT,
        logging.ERROR : apache.APLOG_ERR,
        logging.WARNING : apache.APLOG_WARNING,
        logging.INFO : apache.APLOG_INFO,
        logging.DEBUG : apache.APLOG_ERR,
        logging.NOTSET : apache.APLOG_NOERRNO
        }
    
    def emit(self, record):
        apache.log_error(record.getMessage(), self.typemap[record.levelno])
