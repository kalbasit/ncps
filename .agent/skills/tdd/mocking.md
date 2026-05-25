# When to Mock

Mock at **system boundaries** only:

- External APIs (payment, email, etc.)
- Databases (sometimes - prefer test DB)
- Time/randomness
- File system (sometimes)

Don't mock:

- Your own classes/modules
- Internal collaborators
- Anything you control

## Designing for Mockability

At system boundaries, design interfaces that are easy to mock:

**1. Use dependency injection**

Pass external dependencies in rather than creating them internally:

```typescript
// Easy to mock
function processPayment(order, paymentClient) {
  return paymentClient.charge(order.total);
}

// Hard to mock
function processPayment(order) {
  const client = new StripeClient(process.env.STRIPE_KEY);
  return client.charge(order.total);
}
```

**2. Prefer SDK-style interfaces over generic fetchers**

Create specific functions for each external operation instead of one generic function with conditional logic:

```typescript
// GOOD: Each function is independently mockable
const api = {
  getUser: (id) => fetch(`/users/${id}`),
  getOrders: (userId) => fetch(`/users/${userId}/orders`),
  createOrder: (data) => fetch('/orders', { method: 'POST', body: data }),
};

// BAD: Mocking requires conditional logic inside the mock
const api = {
  fetch: (endpoint, options) => fetch(endpoint, options),
};
```

The SDK approach means:
- Each mock returns one specific shape
- No conditional logic in test setup
- Easier to see which endpoints a test exercises
- Type safety per endpoint

## Go Examples

**Dependency injection via interface:**

```go
// Easy to test — PaymentGateway is an interface
type PaymentGateway interface {
    Charge(ctx context.Context, amount int64) (ChargeID, error)
}

func ProcessPayment(ctx context.Context, order Order, gw PaymentGateway) (ChargeID, error) {
    return gw.Charge(ctx, order.Total)
}

// In tests — implement the interface inline
type fixedGateway struct{ id ChargeID }
func (f fixedGateway) Charge(_ context.Context, _ int64) (ChargeID, error) { return f.id, nil }

// Hard to test — creates its own dependency
func ProcessPayment(ctx context.Context, order Order) (ChargeID, error) {
    gw := stripe.NewClient(os.Getenv("STRIPE_KEY"))
    return gw.Charge(ctx, order.Total)
}
```

**SDK-style interface vs generic fetcher:**

```go
// GOOD: each method is independently implementable in tests
type UserAPI interface {
    GetUser(ctx context.Context, id string) (*User, error)
    ListOrders(ctx context.Context, userID string) ([]*Order, error)
    CreateOrder(ctx context.Context, req CreateOrderReq) (*Order, error)
}

// BAD: test doubles need conditional logic to dispatch correctly
type GenericAPI interface {
    Do(ctx context.Context, method, path string, body any) (json.RawMessage, error)
}
```
