/* basic kind of prefix notation to be turned into SQL later by query-builder
 * notation is:
 * 
 * expr => <op> <expr>|<const> <expr>|<const>
 * op => (& | = < >)
 * const => <string>|<integer>
 * string => ".*"
 * integer => 0..9+
 * 
 * old query: (nmap.type:mail) AND (nmap.to:"bob" OR nmap.to:"adam")
 * new query: & | = "nmap.to" "bob" = "nmap.to" "adam" = "nmap.type" "mail"
 */

/** \file
 * Parser of Store Protocol queries into structures, for later use in SQL queries
 */

#include <stdio.h>
#include <string.h>
#include <stdlib.h>
#include <memmgr.h>
#include <xpl.h>
#include "query-parser.h"

// parse the next token from the query 
// returns 0 on success, 1 on success and end of data, -1 on failure
static int
pull_token(char **query, char **out_token)
{
	char *ptr = *query;
	int result = 0;
	int in_quote = 0;
	int in_escape = 0;
	
	if (*ptr == '\0') return -1; // no data to consume
	
	// go past any spaces
	while (*ptr == ' ') ptr++;
	*out_token = ptr;
	
	// look for the next space or end of string, skipping spaces which occur 
	// within "double quotes" and ignoring escaped quotes.
	while ((*ptr != '\0') && (in_quote || (*ptr != ' '))) {
		if (*ptr == '\\') {
			// look like we're escaping something, unless we're already escaping, 
			// in which case it is a double slash :)
			in_escape = 1 - in_escape;
		} else {
			in_escape = 0;
		}
		if ((*ptr == '"') && (in_escape == 0)) {
			in_quote = 1 - in_quote;
		}
		ptr++;
	}
	if (ptr == *out_token) {
		// this means we consumed spaces but then got to the end of the data
		return -1;
	}
	if (*ptr == '\0') {
		// this means we're on the last token
		result = 1;
	} else {
		// terminate the token we just found
		*ptr = '\0';
	}
	
	// advance our position in the query
	*query = ptr;
	if (result == 0) ++*query;
	
	return result;
}

int
QueryParser_IsOp (const char *token)
{
	const char *ops = "&|<>=!{l~";
	
	// operations must be single characters
	if (token[1] != '\0') return -1;
	
	while (*ops != '\0') {
		if (*ops == token[0]) return 0;
		ops++;
	}
	return -1;
}

#define CHAR_IS_LC_ALPHA(x)	(((x) >= 'a') && ((x) <= 'z'))
#define CHAR_IS_NUMBER(x)	(((x) >= '0') && ((x) <= '9'))
#define CHAR_IS_PERIOD(x)	((x) == '.')

int
QueryParser_IsProperty (const char *token)
{
	const char *ptr;
	int state = 0;
	
	// properties cannot be empty/single chars
	if ((token[0] == '\0') || (token[1] == '\0')) return -1;
	
	ptr = token;
	do {
		switch(state) {
			case 0:
				if (!CHAR_IS_LC_ALPHA(*ptr)) return -1;
				state = 1;
				break;
			case 1:
				if (CHAR_IS_PERIOD(*ptr)) {
					state = 0;
				} else if (!CHAR_IS_LC_ALPHA(*ptr) && !CHAR_IS_NUMBER(*ptr)) {
					return -1;
				}
				break;
			default:
				return -1;
		}
	} while (*(++ptr) != '\0');
	
	// cannot end a property with a period
	if (state != 1) return -1;
	return 0;
}

// when an expression is full, we want to go back up the stack to 
// find the next empty one
static int
rewind_expression(struct expression **e, struct expression *start)
{
	while (((*e)->exp1 != NULL) && ((*e)->exp2 != NULL)) {
		// this expression is full, so we go back up the stack
		--*e;
		if (*e < start) {
			// we've run off the top of the stack... ooops.
			return -1;
		}
	}
	return 0;
}

/**
 * Parse the query which has been set up.
 * \param	state	Parser state we already setup with QueryParserStart
 * \return	0 on success, error codes otherwise.
 */
int
QueryParserRun(struct parser_state *state)
{
	char *token;
	struct expression *current_expression = state->start;
	
	while (pull_token(&(state->query), &token) != -1) {
		if (current_expression < state->start) {
			// ran off the top of our stack, but there were still tokens
			DEBUG_MESSAGE("ERR: tokens remaining in data\n");
			return -1;
		}
		if (QueryParser_IsOp(token) == 0) {
			if (current_expression->op != NULL) {
				// need to find a free expr slot
				struct expression *next_expression = ++(state->last);
				
				// check we're not off the end of the array
				if (state->last > (state->start + state->entries)) {
					DEBUG_MESSAGE("ERR: ran off end of array\n");
					return -1;
				}
				
				// link the current expression to the new one
				if (current_expression->exp1 == NULL) {
					current_expression->exp1 = next_expression;
				} else if (current_expression->exp2 == NULL) {
					current_expression->exp2 = next_expression;
				} else {
					// bad expression with three args or something
					DEBUG_MESSAGE("ERR: Too many args\n");
					return -1;
				}
				// advance to the new expression
				current_expression = next_expression;
			}
			current_expression->op = token;
		} else {
			// find the next last expression with free slots
			if (rewind_expression(&current_expression, state->start)) {
				DEBUG_MESSAGE("ERR: ran off the start of the array\n");
				return -1;
			}
			if (current_expression->op == NULL) {
				// we can't set parameters before we have an operator...
				DEBUG_MESSAGE("ERR: parameters received before operator\n");
				return -1;
			}
			if (current_expression->exp1 == NULL) {
				current_expression->exp1 = token;
				current_expression->exp1_const = 1;
			} else if (current_expression->exp2 == NULL) {
				current_expression->exp2 = token;
				current_expression->exp2_const = 1;
				
				// ignore errors here, since when we fill the last slot
				// we pop off top of the stack. That's not an error if 
				// there are no more tokens (see check at top of loop)
				rewind_expression(&current_expression, state->start);
			}
		}
	}
	
	if (current_expression >= state->start) {
		// we didn't fill all our slots...
		DEBUG_MESSAGE("ERR: not enough parameters received\n");
		return -1;
	}
	
	return 0;
}

void
output_expression(struct expression *e)
{
	// want to do a printable version of this expression.
	// output left side first, then the operator, then the right side
	XplConsolePrintf("(");
	if (e->exp1_const) {
		XplConsolePrintf("%s", (char *)e->exp1);
	} else {
		output_expression((struct expression *)e->exp1);
	}
	XplConsolePrintf(" %s ", e->op);
	if (e->exp2_const) {
		XplConsolePrintf("%s", (char *)e->exp2);
	} else {
		output_expression((struct expression *)e->exp2);
	}
	XplConsolePrintf(")");
}

/**
 * Start a new Query parser.
 * \param	state	New parser state - this gets initialised for future use
 * \param	query	String containing the query to parse
 * \param	max_expr	Maximum expression parts we will allow
 * \return	0 if successfully allocated, error codes otherwise
 */
int
QueryParserStart(struct parser_state *state, const char *query, int max_expr) {
	size_t q_size;
	
	state->entries = max_expr;
	q_size = sizeof(struct expression) * (max_expr + 1);
	
	state->start = MemNew0(struct expression, max_expr);
	if (state->start == NULL) return -1;
	state->last = state->start;
	
	state->query = MemStrdup(query);
	if (state->query == NULL) {
		MemFree(state->start);
		return -2;
	}
	
	return 0;
}

/**
 * Finish a query parser. This de-allocates any memory used internally, and should always be called
 * on any parser which is set up.
 * \param	state	The state to clean up.
 */
void
QueryParserFinish(struct parser_state *state) {
	if (state->start != NULL) {
		MemFree(state->start);
	}
	if (state->query != NULL) {
		MemFree(state->query);
	}
	state->start = state->last = NULL;
	state->entries = 0;
}

#if 0

static const char *test = 
	"& & | = \"nmap.to\" \"bob\" = \"nmap.to\" \"adam \\\"foo\\\" jones\" = \"nmap.type\" \"mail\" = nmap.guid 123";

int
QueryParserTest(void)
{
	struct parser_state parser;
	
	DEBUG_MESSAGE("Test query: %s\n\n", test);
	
	QueryParserStart(&parser, test, 50);
	
	if (QueryParserRun(&parser)) {
		DEBUG_MESSAGE("ERROR: Parsing error\n");
	} else {
		output_expression(parser.start);
		printf("\n");
	}
	
	QueryParserFinish(&parser);
	
	return 0;
}

#endif
