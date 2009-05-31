/* query parser bits
 */

#ifndef QUERY_PARSER_H
#define QUERY_PARSER_H

#define	DEBUG_MESSAGE(...)	

struct expression {
	char *op;
	
	void *exp1;
	void *exp2;
	
	int exp1_const;
	int exp2_const;
};

struct parser_state {
	struct expression *start;
	struct expression *last;
	int entries;
	char *query;
	char *query_ptr;
};

int	QueryParserStart(struct parser_state *state, const char *query, int max_expr);
int	QueryParserRun(struct parser_state *state);
void	QueryParserFinish(struct parser_state *state);

int	QueryParser_IsOp (const char *token);
int	QueryParser_IsProperty (const char *token);

void	output_expression(struct expression *e);

#endif
